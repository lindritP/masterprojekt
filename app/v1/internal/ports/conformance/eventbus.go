package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// EventBusFixture provides a live broker with a queue reserved for the suite.
type EventBusFixture struct {
	Bus ports.EventBus

	// NewConsumer returns a consumer of the suite's queue.
	NewConsumer func(t *testing.T) ports.EventConsumer

	// EventType is routed to the suite's queue, and only there.
	EventType string

	// LeaseDuration is how long the broker waits for a settlement before
	// redelivering. Zero means the broker has no such lease.
	LeaseDuration time.Duration

	// Purge empties the suite's queue and its dead-letter queue.
	Purge func(t *testing.T)

	// ReadDeadLetter returns the next dead-lettered event, or an error if
	// none arrives within the context's deadline.
	ReadDeadLetter func(ctx context.Context) (ports.Event, error)
}

type payload struct {
	Run string `json:"run"`
	Seq int    `json:"seq"`
}

// TestEventBus runs the EventBus / EventConsumer semantics of SRS §7.4.
func TestEventBus(t *testing.T, f EventBusFixture) {
	ctx := context.Background()

	publish := func(t *testing.T, run, key string, seq int, id string) {
		t.Helper()
		data, _ := json.Marshal(payload{Run: run, Seq: seq})
		if id == "" {
			id = fmt.Sprintf("%s-%s-%d", run, key, seq)
		}
		mustOK(t, f.Bus.Publish(ctx, ports.Event{
			Type: f.EventType, Source: "/ccsra/conformance", ID: id, Time: time.Now().UTC(),
			CorrelationID: run, Data: data,
		}, key))
	}

	// consume runs handler for every delivery until stop is called.
	consume := func(t *testing.T, handler func(ports.Delivery) ports.Outcome) (stop func()) {
		t.Helper()
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		c := f.NewConsumer(t)
		go func() {
			done <- c.Subscribe(cctx, func(_ context.Context, d ports.Delivery) ports.Outcome { return handler(d) })
		}()
		return func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("Subscribe returned %v", err)
			}
		}
	}

	// recorder collects deliveries of one run.
	type recorder struct {
		mu         sync.Mutex
		deliveries []ports.Delivery
	}
	seqOf := func(d ports.Delivery) int {
		var p payload
		_ = json.Unmarshal(d.Event.Data, &p)
		return p.Seq
	}
	waitFor := func(t *testing.T, r *recorder, n int, within time.Duration) []ports.Delivery {
		t.Helper()
		deadline := time.Now().Add(within)
		for time.Now().Before(deadline) {
			r.mu.Lock()
			got := slices.Clone(r.deliveries)
			r.mu.Unlock()
			if len(got) >= n {
				return got
			}
			time.Sleep(50 * time.Millisecond)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		t.Fatalf("received %d deliveries within %s, want %d", len(r.deliveries), within, n)
		return nil
	}
	record := func(r *recorder, outcome func(ports.Delivery) ports.Outcome) func(ports.Delivery) ports.Outcome {
		return func(d ports.Delivery) ports.Outcome {
			r.mu.Lock()
			r.deliveries = append(r.deliveries, d)
			r.mu.Unlock()
			return outcome(d)
		}
	}
	ack := func(ports.Delivery) ports.Outcome { return ports.OutcomeAck }

	t.Run("Publish_AtLeastOnceDelivery", func(t *testing.T) {
		f.Purge(t)
		run := RunID()
		for i := range 10 {
			publish(t, run, fmt.Sprintf("key-%d", i), i, "")
		}
		r := &recorder{}
		stop := consume(t, record(r, ack))
		got := waitFor(t, r, 10, 15*time.Second)
		stop()
		seen := map[string]bool{}
		for _, d := range got {
			seen[d.Event.ID] = true
		}
		if len(seen) != 10 {
			t.Errorf("%d distinct events delivered, want 10", len(seen))
		}
	})

	t.Run("SamePartitionKey_PreservesOrder", func(t *testing.T) {
		f.Purge(t)
		run := RunID()
		for i := range 20 {
			publish(t, run, "one-key", i, "")
		}
		r := &recorder{}
		stop := consume(t, record(r, ack))
		got := waitFor(t, r, 20, 15*time.Second)
		stop()
		for i, d := range got[:20] {
			if seqOf(d) != i {
				t.Fatalf("delivery %d carried seq %d: order not preserved", i, seqOf(d))
			}
		}
	})

	t.Run("DifferentPartitionKeys_MayInterleave", func(t *testing.T) {
		f.Purge(t)
		run := RunID()
		for i := range 4 {
			for k := range 5 {
				publish(t, run, fmt.Sprintf("key-%d", k), i, "")
			}
		}
		r := &recorder{}
		stop := consume(t, record(r, ack))
		got := waitFor(t, r, 20, 15*time.Second)
		stop()
		// No order across keys is asserted; within each key it must hold.
		last := map[string]int{}
		for _, d := range got {
			if prev, ok := last[d.PartitionKey]; ok && seqOf(d) <= prev {
				t.Errorf("key %s: seq %d after %d", d.PartitionKey, seqOf(d), prev)
			}
			last[d.PartitionKey] = seqOf(d)
		}
	})

	t.Run("Retry_IncrementsDeliveryCount", func(t *testing.T) {
		f.Purge(t)
		publish(t, RunID(), "retry", 0, "")
		r := &recorder{}
		stop := consume(t, record(r, func(d ports.Delivery) ports.Outcome {
			if d.DeliveryCount == 1 {
				return ports.OutcomeRetry
			}
			return ports.OutcomeAck
		}))
		got := waitFor(t, r, 2, 15*time.Second)
		stop()
		if got[0].DeliveryCount != 1 || got[1].DeliveryCount != 2 {
			t.Errorf("delivery counts %d, %d; want 1, 2", got[0].DeliveryCount, got[1].DeliveryCount)
		}
	})

	t.Run("DeadLetter_RemovesFromMainFlow", func(t *testing.T) {
		f.Purge(t)
		run := RunID()
		publish(t, run, "dead", 0, "")
		r := &recorder{}
		stop := consume(t, record(r, func(ports.Delivery) ports.Outcome { return ports.OutcomeDeadLetter }))
		got := waitFor(t, r, 1, 15*time.Second)
		time.Sleep(2 * time.Second) // give a wrongful redelivery the chance to show up
		stop()
		r.mu.Lock()
		n := len(r.deliveries)
		r.mu.Unlock()
		if n != 1 {
			t.Errorf("dead-lettered message delivered %d times, want 1", n)
		}
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		e, err := f.ReadDeadLetter(dctx)
		mustOK(t, err)
		if e.ID != got[0].Event.ID {
			t.Errorf("dead-letter queue holds %q, want %q", e.ID, got[0].Event.ID)
		}
	})

	t.Run("MaxDeliveryCount_DeadLetters", func(t *testing.T) {
		f.Purge(t)
		publish(t, RunID(), "poison", 0, "")
		r := &recorder{}
		stop := consume(t, record(r, func(ports.Delivery) ports.Outcome { return ports.OutcomeRetry }))
		waitFor(t, r, 2, 15*time.Second)
		// Wait until redeliveries stop.
		var n int
		for stable := 0; stable < 3; {
			time.Sleep(time.Second)
			r.mu.Lock()
			if len(r.deliveries) == n {
				stable++
			} else {
				n, stable = len(r.deliveries), 0
			}
			r.mu.Unlock()
		}
		stop()
		t.Logf("message was delivered %d times before being dead-lettered", n)
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, err := f.ReadDeadLetter(dctx)
		mustOK(t, err)
	})

	t.Run("LeaseRenewal_SurvivesSlowHandler", func(t *testing.T) {
		f.Purge(t)
		publish(t, RunID(), "slow", 0, "")
		hold := max(2*f.LeaseDuration, 3*time.Second)
		r := &recorder{}
		stop := consume(t, record(r, func(ports.Delivery) ports.Outcome {
			time.Sleep(hold)
			return ports.OutcomeAck
		}))
		waitFor(t, r, 1, 15*time.Second)
		time.Sleep(hold + 2*time.Second)
		stop()
		r.mu.Lock()
		defer r.mu.Unlock()
		if len(r.deliveries) != 1 {
			t.Errorf("slow message delivered %d times, want 1", len(r.deliveries))
		}
	})

	t.Run("DuplicateDelivery_IsToleratedByHandler", func(t *testing.T) {
		f.Purge(t)
		run := RunID()
		publish(t, run, "dup", 0, run+"-same-id")
		publish(t, run, "dup", 0, run+"-same-id")
		var mu sync.Mutex
		effects := map[string]int{}
		r := &recorder{}
		stop := consume(t, record(r, func(d ports.Delivery) ports.Outcome {
			mu.Lock()
			defer mu.Unlock()
			if effects[d.Event.ID] == 0 { // idempotent effect, keyed by event id (FR-403)
				effects[d.Event.ID]++
			}
			return ports.OutcomeAck
		}))
		waitFor(t, r, 1, 15*time.Second)
		time.Sleep(time.Second)
		stop()
		if effects[run+"-same-id"] != 1 {
			t.Errorf("recorded %d effects, want 1", effects[run+"-same-id"])
		}
	})
}
