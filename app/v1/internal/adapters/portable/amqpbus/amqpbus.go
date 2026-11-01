// Package amqpbus is the portable EventBus / EventConsumer adapter (SRS §7.3):
// AMQP 1.0, tested against RabbitMQ 4 with quorum queues.
//
// Events travel as structured CloudEvents (SRS §5.6) and are published to
// /exchanges/<exchange>/<event type>. The broker topology — queues, delivery
// limit, dead-letter exchange — is configuration, not code
// (deploy/local/rabbitmq/definitions.json), just as SQS queues are IaC.
//
// Ordering per partition key (FR-405) is provided by the consumer, not the
// broker: messages are dispatched to MaxInFlight shards by key hash, and each
// shard processes its messages one at a time. Unlike SQS FIFO message groups,
// a retried message is requeued, so later messages of the same key can
// overtake it. That difference is recorded in the coupling register.
package amqpbus

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/Azure/go-amqp"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Config mirrors CCSRA_BROKER_* (SRS §9.3).
type Config struct {
	URL         string // e.g. amqp://localhost:5672
	User        string
	Password    string
	VHost       string
	Exchange    string // publish target, e.g. ccsra.events
	Queue       string // consume source, e.g. ccsra.validation
	MaxInFlight int    // CCSRA_MAX_INFLIGHT (FR-408)
	Logger      *slog.Logger
}

// Bus implements ports.EventBus and ports.EventConsumer over one connection.
type Bus struct {
	cfg     Config
	log     *slog.Logger
	conn    *amqp.Conn
	session *amqp.Session

	mu      sync.Mutex
	senders map[string]*amqp.Sender // by routing key
}

var (
	_ ports.EventBus      = (*Bus)(nil)
	_ ports.EventConsumer = (*Bus)(nil)
)

const (
	contentType     = "application/cloudevents+json"
	partitionKeyApp = "cloudEvents:partitionkey"
	settleTimeout   = 10 * time.Second
)

func Dial(ctx context.Context, cfg Config) (*Bus, error) {
	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 8
	}
	conn, err := amqp.Dial(ctx, cfg.URL, &amqp.ConnOptions{
		SASLType: amqp.SASLTypePlain(cfg.User, cfg.Password),
		HostName: "vhost:" + cfg.VHost,
	})
	if err != nil {
		return nil, fmt.Errorf("amqp dial: %w", err)
	}
	session, err := conn.NewSession(ctx, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("amqp session: %w", err)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Bus{cfg: cfg, log: log, conn: conn, session: session, senders: map[string]*amqp.Sender{}}, nil
}

func (b *Bus) Close() error { return b.conn.Close() }

// envelope is the CloudEvents 1.0 structured form of an event (SRS §5.6).
type envelope struct {
	SpecVersion     string          `json:"specversion"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	ID              string          `json:"id"`
	Time            time.Time       `json:"time"`
	Subject         string          `json:"subject,omitempty"`
	DataContentType string          `json:"datacontenttype"`
	PartitionKey    string          `json:"partitionkey"`
	CorrelationID   string          `json:"correlationid,omitempty"`
	Data            json.RawMessage `json:"data"`
}

func (b *Bus) Publish(ctx context.Context, e ports.Event, partitionKey string) error {
	body, err := json.Marshal(envelope{
		SpecVersion: "1.0", Type: e.Type, Source: e.Source, ID: e.ID, Time: e.Time, Subject: e.Subject,
		DataContentType: "application/json", PartitionKey: partitionKey, CorrelationID: e.CorrelationID, Data: e.Data,
	})
	if err != nil {
		return err
	}
	sender, err := b.sender(ctx, e.Type)
	if err != nil {
		return err
	}
	ct := contentType
	msg := &amqp.Message{
		Header:                &amqp.MessageHeader{Durable: true},
		Properties:            &amqp.MessageProperties{MessageID: e.ID, CorrelationID: e.CorrelationID, ContentType: &ct},
		ApplicationProperties: map[string]any{partitionKeyApp: partitionKey},
		Data:                  [][]byte{body},
	}
	if err := sender.Send(ctx, msg, nil); err != nil {
		return fmt.Errorf("publish %s: %w", e.Type, err)
	}
	return nil
}

func (b *Bus) sender(ctx context.Context, routingKey string) (*amqp.Sender, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.senders[routingKey]; ok {
		return s, nil
	}
	addr := fmt.Sprintf("/exchanges/%s/%s", url.PathEscape(b.cfg.Exchange), url.PathEscape(routingKey))
	s, err := b.session.NewSender(ctx, addr, nil)
	if err != nil {
		return nil, fmt.Errorf("amqp sender %s: %w", addr, err)
	}
	b.senders[routingKey] = s
	return s, nil
}

// Subscribe consumes the configured queue until ctx is cancelled. At most
// MaxInFlight messages are unsettled at once (link credit). Work in progress
// is finished and settled before Subscribe returns (FR-016).
func (b *Bus) Subscribe(ctx context.Context, handler func(context.Context, ports.Delivery) ports.Outcome) error {
	receiver, err := b.session.NewReceiver(ctx, "/queues/"+url.PathEscape(b.cfg.Queue), &amqp.ReceiverOptions{Credit: int32(b.cfg.MaxInFlight)})
	if err != nil {
		return fmt.Errorf("amqp receiver: %w", err)
	}
	defer receiver.Close(context.Background())

	shards := make([]chan *amqp.Message, b.cfg.MaxInFlight)
	var wg sync.WaitGroup
	for i := range shards {
		shards[i] = make(chan *amqp.Message, b.cfg.MaxInFlight)
		wg.Go(func() {
			for msg := range shards[i] {
				b.process(ctx, receiver, msg, handler)
			}
		})
	}
	defer func() {
		for _, s := range shards {
			close(s)
		}
		wg.Wait()
	}()

	for {
		msg, err := receiver.Receive(ctx, nil)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("amqp receive: %w", err)
		}
		key, _ := msg.ApplicationProperties[partitionKeyApp].(string)
		h := fnv.New32a()
		h.Write([]byte(key))
		shards[h.Sum32()%uint32(len(shards))] <- msg
	}
}

func (b *Bus) process(ctx context.Context, r *amqp.Receiver, msg *amqp.Message, handler func(context.Context, ports.Delivery) ports.Outcome) {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()

	var env envelope
	if err := json.Unmarshal(msg.GetData(), &env); err != nil {
		_ = r.RejectMessage(settleCtx, msg, &amqp.Error{Condition: amqp.ErrCondDecodeError, Description: err.Error()})
		return
	}
	d := ports.Delivery{
		MessageID:     env.ID,
		DeliveryCount: 1,
		PartitionKey:  env.PartitionKey,
		Event: ports.Event{
			Type: env.Type, Source: env.Source, ID: env.ID, Time: env.Time, Subject: env.Subject,
			CorrelationID: env.CorrelationID, Data: env.Data,
		},
	}
	if msg.Header != nil {
		d.DeliveryCount = int(msg.Header.DeliveryCount) + 1
	}

	var err error
	switch handler(context.WithoutCancel(ctx), d) {
	case ports.OutcomeAck:
		err = r.AcceptMessage(settleCtx, msg)
	case ports.OutcomeRetry:
		// delivery-failed increments the delivery count; the quorum queue
		// dead-letters once its delivery limit is exceeded.
		err = r.ModifyMessage(settleCtx, msg, &amqp.ModifyMessageOptions{DeliveryFailed: true})
	case ports.OutcomeDeadLetter:
		err = r.RejectMessage(settleCtx, msg, nil)
	}
	if err != nil {
		// The message stays unsettled and is redelivered after reconnect;
		// consumers are idempotent (FR-023).
		b.log.Warn("settling message failed", "messageId", d.MessageID, "error", err)
	}
}
