// Command identity-gateway is the public HTTP edge and the CP-5 (identity)
// carrier (SRS §4.2), in the abstracted variant V1.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/gateway"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/wiring/identity"
)

func main() { os.Exit(run()) }

func run() int {
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz and exit")
	flag.Parse()

	var env platform.Env
	common := platform.ReadCommon(&env, "identity-gateway")
	if *healthcheck {
		platform.HealthcheckFlag(common.HTTPAddr)
	}
	log := platform.NewLogger(common.Service, common.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	idSetup := identity.FromEnv(&env)
	timeout := time.Duration(env.Int("CCSRA_UPSTREAM_TIMEOUT_MS", 5000)) * time.Millisecond
	client := &http.Client{Timeout: timeout}
	srv := &gateway.Server{
		Cases:        &gateway.Upstream{Name: "case-service", Base: env.Required("CCSRA_CASE_SERVICE_URL"), Timeout: timeout, Client: client},
		Intake:       &gateway.Upstream{Name: "intake-service", Base: env.Required("CCSRA_INTAKE_SERVICE_URL"), Timeout: timeout, Client: client},
		MaxBodyBytes: int64(env.Int("CCSRA_MAX_BODY_BYTES", 65536)),
		RateLimit:    rate.Limit(env.Int("CCSRA_RATE_LIMIT_RPS", 50)),
		CORSOrigins:  env.List("CCSRA_CORS_ALLOWED_ORIGINS"),
		Log:          log,
	}
	if err := env.Err(); err != nil {
		log.Error("startup failed", "error", err)
		return 1
	}
	verifier, err := idSetup.Open(ctx)
	if err != nil {
		log.Error("identity provider unavailable", "error", err)
		return 1
	}
	srv.Verifier = verifier

	metrics := platform.NewMetrics()
	metrics.ReportAdapters(log, common.Variant, common.Target, map[string]string{"identity": idSetup.Name})
	var health platform.Health
	for _, u := range []*gateway.Upstream{srv.Cases, srv.Intake} {
		health.Check(u.Name, func(ctx context.Context) error {
			resp, err := u.Send(ctx, http.MethodGet, "/healthz", http.Header{}, nil)
			if err != nil {
				return err
			}
			resp.Body.Close()
			return nil
		})
	}

	mux := http.NewServeMux()
	platform.OpsRoutes(mux, &health, metrics)
	srv.Routes(mux)

	handler := srv.CORS(platform.Correlation(platform.Observe(log, metrics, mux)))
	if err := platform.Serve(ctx, log, common.HTTPAddr, common.DrainTimeout, handler); err != nil {
		log.Error("server failed", "error", err)
		return 1
	}
	return 0
}
