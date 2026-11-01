// Command intake-service is the owner of document objects and the CP-2
// carrier (SRS §4.4), in the abstracted variant V1.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/intake"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/wiring/broker"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/wiring/secrets"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/wiring/storage"
)

func main() { os.Exit(run()) }

func run() int {
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz and exit")
	flag.Parse()

	var env platform.Env
	common := platform.ReadCommon(&env, "intake-service")
	if *healthcheck {
		platform.HealthcheckFlag(common.HTTPAddr)
	}
	log := platform.NewLogger(common.Service, common.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	secretProvider, secretsName := secrets.FromEnv(&env)
	busSetup := broker.FromEnv(ctx, &env, secretProvider, log, false)
	caseServiceURL := env.Required("CCSRA_CASE_SERVICE_URL")
	srv := &intake.Server{
		MaxObjectBytes: int64(env.Int("CCSRA_MAX_OBJECT_BYTES", 20971520)),
		AllowedMime:    env.List("CCSRA_ALLOWED_MIME", "application/pdf"),
		PresignTTL:     env.Seconds("CCSRA_PRESIGN_TTL_SECONDS", 900),
		MaxBodyBytes:   64 << 10,
		Log:            log,
	}
	stores, err := storage.FromEnv(ctx, &env, secretProvider) // also reports earlier config problems
	if err != nil {
		log.Error("startup failed", "error", err)
		return 1
	}
	bus, err := busSetup.Open(ctx)
	if err != nil {
		log.Error("broker unavailable", "error", err)
		return 1
	}
	defer bus.Close()

	srv.Cases = platform.NewClient(caseServiceURL, common.Service, 5*time.Second)
	srv.Public, srv.Internal, srv.Bus = stores.Public, stores.Internal, bus

	metrics := platform.NewMetrics()
	metrics.ReportAdapters(log, common.Variant, common.Target, map[string]string{
		"storage": stores.Name, "broker": busSetup.Name, "secrets": secretsName,
	})
	var health platform.Health
	health.Check("case-service", func(ctx context.Context) error {
		_, err := srv.Cases.Do(ctx, http.MethodGet, "/healthz", nil, nil, nil)
		return err
	})

	mux := http.NewServeMux()
	platform.OpsRoutes(mux, &health, metrics)
	srv.Routes(mux)

	handler := platform.Correlation(platform.InternalContext(platform.Observe(log, metrics, mux)))
	if err := platform.Serve(ctx, log, common.HTTPAddr, common.DrainTimeout, handler); err != nil {
		log.Error("server failed", "error", err)
		return 1
	}
	return 0
}
