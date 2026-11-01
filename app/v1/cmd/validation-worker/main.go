// Command validation-worker is the queue consumer that validates documents
// and the CP-4 carrier (SRS §4.5), in the abstracted variant V1.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/pipeline"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/wiring/broker"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/wiring/secrets"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/worker"
)

func main() { os.Exit(run()) }

func run() int {
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz and exit")
	flag.Parse()

	var env platform.Env
	common := platform.ReadCommon(&env, "validation-worker")
	if *healthcheck {
		platform.HealthcheckFlag(common.HTTPAddr)
	}
	log := platform.NewLogger(common.Service, common.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	secretProvider, secretsName := secrets.FromEnv(&env)
	busSetup := broker.FromEnv(ctx, &env, secretProvider, log, true)
	inFlight := env.Int("CCSRA_MAX_INFLIGHT", 8)
	limits := pipeline.Limits{
		MaxBytes: int64(env.Int("CCSRA_MAX_OBJECT_BYTES", 20971520)),
		MaxPages: env.Int("CCSRA_MAX_PAGES", 50),
		Timeout:  env.Seconds("CCSRA_VALIDATION_TIMEOUT_SECONDS", 30),
	}
	w := &worker.Worker{
		MaxObjectBytes:   limits.MaxBytes,
		MaxDeliveryCount: env.Int("CCSRA_MAX_DELIVERY_COUNT", 5),
		Log:              log,
		HTTP:             &http.Client{Timeout: 60 * time.Second},
	}
	intakeURL := env.Required("CCSRA_INTAKE_SERVICE_URL")
	caseURL := env.Required("CCSRA_CASE_SERVICE_URL")
	// AMQP 1.0 has no processing lease; FR-407 is met by the broker keeping
	// unsettled messages with the consumer. The flag is read for SQS (B6).
	_ = env.Bool("CCSRA_LEASE_RENEW_ENABLED", true)
	if err := env.Err(); err != nil {
		log.Error("startup failed", "error", err)
		return 1
	}

	validator, err := pipeline.New(limits, inFlight)
	if err != nil {
		log.Error("pdf engine unavailable", "error", err)
		return 1
	}
	defer validator.Close()
	bus, err := busSetup.Open(ctx)
	if err != nil {
		log.Error("broker unavailable", "error", err)
		return 1
	}
	defer bus.Close()

	w.Validator, w.Bus = validator, bus
	w.Intake = platform.NewClient(intakeURL, common.Service, 30*time.Second)
	w.Cases = platform.NewClient(caseURL, common.Service, 30*time.Second)

	metrics := platform.NewMetrics()
	w.RegisterMetrics(metrics.Registry)
	metrics.ReportAdapters(log, common.Variant, common.Target, map[string]string{"broker": busSetup.Name, "secrets": secretsName})
	var health platform.Health
	for name, c := range map[string]*platform.Client{"intake-service": w.Intake, "case-service": w.Cases} {
		health.Check(name, func(ctx context.Context) error {
			_, err := c.Do(ctx, http.MethodGet, "/healthz", nil, nil, nil)
			return err
		})
	}
	mux := http.NewServeMux()
	platform.OpsRoutes(mux, &health, metrics) // no business endpoints (FR-400)

	// Consume until SIGTERM; Subscribe finishes in-flight messages before it
	// returns (FR-016). The ops server keeps answering until then.
	consumed := make(chan error, 1)
	go func() { consumed <- bus.Subscribe(ctx, w.Handle) }()
	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	served := make(chan error, 1)
	go func() {
		served <- platform.Serve(serveCtx, log, common.HTTPAddr, common.DrainTimeout, platform.Correlation(platform.Observe(log, metrics, mux)))
	}()

	exit := 0
	select {
	case err := <-consumed:
		if err != nil {
			log.Error("consumer stopped", "error", err)
			exit = 1
		}
	case err := <-served:
		log.Error("ops server failed", "error", err)
		return 1
	}
	stopServe()
	if err := <-served; err != nil {
		log.Error("ops server shutdown", "error", err)
	}
	log.Info("stopped")
	return exit
}
