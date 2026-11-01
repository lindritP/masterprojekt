// Command case-service is the owner of relational case state and the CP-3
// carrier (SRS §4.3), in the abstracted variant V1.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/caseservice"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/wiring/broker"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/wiring/repository"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/wiring/secrets"
)

func main() { os.Exit(run()) }

func run() int {
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz and exit")
	flag.Parse()

	var env platform.Env
	common := platform.ReadCommon(&env, "case-service")
	if *healthcheck {
		platform.HealthcheckFlag(common.HTTPAddr)
	}
	log := platform.NewLogger(common.Service, common.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	secretProvider, secretsName := secrets.FromEnv(&env)
	repoSetup := repository.FromEnv(ctx, &env, secretProvider)
	busSetup := broker.FromEnv(ctx, &env, secretProvider, log, false)
	policy := domain.DocumentPolicy{
		MaxDocuments: env.Int("CCSRA_MAX_DOCUMENTS_PER_CASE", 10),
		MaxBytes:     int64(env.Int("CCSRA_MAX_OBJECT_BYTES", 20971520)),
		AllowedMime:  env.List("CCSRA_ALLOWED_MIME", "application/pdf"),
	}
	if err := env.Err(); err != nil {
		log.Error("startup failed", "error", err)
		return 1
	}

	repo, err := repoSetup.Open(ctx)
	if err != nil {
		log.Error("database unavailable", "error", err)
		return 1
	}
	defer repo.Close()
	bus, err := busSetup.Open(ctx)
	if err != nil {
		log.Error("broker unavailable", "error", err)
		return 1
	}
	defer bus.Close()

	metrics := platform.NewMetrics()
	metrics.ReportAdapters(log, common.Variant, common.Target, map[string]string{
		"repository": repoSetup.Name, "broker": busSetup.Name, "secrets": secretsName,
	})
	var health platform.Health
	health.Check("database", repo.Ping)

	mux := http.NewServeMux()
	platform.OpsRoutes(mux, &health, metrics)
	(&caseservice.Server{Repo: repo, Bus: bus, Policy: policy, MaxBodyBytes: 64 << 10, Log: log}).Routes(mux)

	handler := platform.Correlation(platform.InternalContext(platform.Observe(log, metrics, mux)))
	if err := platform.Serve(ctx, log, common.HTTPAddr, common.DrainTimeout, handler); err != nil {
		log.Error("server failed", "error", err)
		return 1
	}
	return 0
}
