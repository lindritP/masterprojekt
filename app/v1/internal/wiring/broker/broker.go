// Package broker selects the EventBus / EventConsumer adapter (CCSRA_ADAPTER_BROKER).
package broker

import (
	"context"
	"log/slog"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/adapters/portable/amqpbus"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Broker publishes and, if a subscription is configured, consumes events.
type Broker interface {
	ports.EventBus
	ports.EventConsumer
	Close() error
}

// Setup is a validated configuration, ready to connect.
type Setup struct {
	Name string
	cfg  amqpbus.Config
}

// FromEnv reads CCSRA_ADAPTER_BROKER and CCSRA_BROKER_*. withSubscription
// also reads the consumer settings of validation-worker.
func FromEnv(ctx context.Context, e *platform.Env, secrets ports.SecretProvider, log *slog.Logger, withSubscription bool) Setup {
	name := e.Adapter("CCSRA_ADAPTER_BROKER", "amqp")
	cfg := amqpbus.Config{
		URL:      e.Required("CCSRA_BROKER_URL"),
		User:     e.Required("CCSRA_BROKER_USER"),
		Password: e.Secret(ctx, secrets, "CCSRA_BROKER_PASSWORD"),
		VHost:    e.String("CCSRA_BROKER_VHOST", "/"),
		Exchange: e.String("CCSRA_BROKER_EXCHANGE", "ccsra.events"),
		Logger:   log,
	}
	if withSubscription {
		cfg.Queue = e.String("CCSRA_SUBSCRIPTION", "validation")
		cfg.MaxInFlight = e.Int("CCSRA_MAX_INFLIGHT", 8)
	}
	return Setup{Name: name, cfg: cfg}
}

// Open connects.
func (s Setup) Open(ctx context.Context) (Broker, error) {
	return amqpbus.Dial(ctx, s.cfg)
}
