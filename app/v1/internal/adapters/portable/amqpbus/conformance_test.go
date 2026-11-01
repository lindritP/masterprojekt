package amqpbus

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Azure/go-amqp"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports/conformance"
)

// The suite uses its own queue (ccsra.conformance), configured exactly like
// ccsra.validation, so it never interferes with a running worker.
const (
	suiteQueue = "ccsra.conformance"
	suiteDLQ   = "ccsra.conformance.dlq"
)

func TestConformance(t *testing.T) {
	conformance.RequireEnabled(t)
	cfg := Config{
		URL:         env("CCSRA_BROKER_URL", "amqp://localhost:5672"),
		User:        env("CCSRA_BROKER_USER", "ccsra"),
		Password:    env("CCSRA_BROKER_PASSWORD", "ccsra-local"),
		VHost:       env("CCSRA_BROKER_VHOST", "ccsra"),
		Exchange:    "ccsra.events",
		Queue:       suiteQueue,
		MaxInFlight: 4,
	}
	ctx := context.Background()
	bus, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close() })

	conformance.TestEventBus(t, conformance.EventBusFixture{
		Bus:       bus,
		EventType: "conformance.event.v1",
		NewConsumer: func(t *testing.T) ports.EventConsumer {
			c, err := Dial(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			return c
		},
		LeaseDuration: 0, // AMQP 1.0 on RabbitMQ: unsettled messages stay with the consumer
		Purge: func(t *testing.T) {
			for _, q := range []string{suiteQueue, suiteDLQ} {
				if err := drain(ctx, bus, q); err != nil {
					t.Fatal(err)
				}
			}
		},
		ReadDeadLetter: func(ctx context.Context) (ports.Event, error) {
			r, err := bus.session.NewReceiver(ctx, "/queues/"+suiteDLQ, &amqp.ReceiverOptions{Credit: 1})
			if err != nil {
				return ports.Event{}, err
			}
			defer r.Close(context.Background())
			msg, err := r.Receive(ctx, nil)
			if err != nil {
				return ports.Event{}, err
			}
			var env envelope
			if err := json.Unmarshal(msg.GetData(), &env); err != nil {
				return ports.Event{}, err
			}
			return ports.Event{ID: env.ID, Type: env.Type}, r.AcceptMessage(ctx, msg)
		},
	})
}

// drain accepts every message currently in queue q.
func drain(ctx context.Context, b *Bus, q string) error {
	r, err := b.session.NewReceiver(ctx, "/queues/"+q, &amqp.ReceiverOptions{Credit: 100})
	if err != nil {
		return err
	}
	defer r.Close(context.Background())
	for {
		rctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		msg, err := r.Receive(rctx, nil)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := r.AcceptMessage(ctx, msg); err != nil {
			return err
		}
	}
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
