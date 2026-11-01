package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics is a service's Prometheus registry with the shared HTTP metrics.
type Metrics struct {
	Registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	adapters *prometheus.GaugeVec
}

// NewMetrics creates the registry exposed on /metrics (FR-011).
func NewMetrics() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ccsra_http_requests_total", Help: "HTTP requests by route pattern and status code.",
		}, []string{"route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ccsra_http_request_duration_seconds", Help: "HTTP request duration by route pattern.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
		adapters: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ccsra_adapter_info", Help: "Resolved adapter per port (AR-010); value is always 1.",
		}, []string{"port", "adapter"}),
	}
	m.Registry.MustRegister(m.requests, m.duration, m.adapters,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// ReportAdapters logs the resolved adapter set and exposes it as a gauge, so
// every measurement run can be tied to the configuration it exercised (AR-010).
func (m *Metrics) ReportAdapters(log *slog.Logger, variant, target string, adapters map[string]string) {
	args := []any{"variant", variant, "target", target}
	for port, name := range adapters {
		m.adapters.With(prometheus.Labels{"port": port, "adapter": name}).Set(1)
		args = append(args, "adapter."+port, name)
	}
	log.Info("resolved adapters", args...)
}

// Health runs readiness checks against a service's own dependencies (FR-010).
type Health struct {
	mu     sync.Mutex
	checks map[string]func(context.Context) error
}

// Check registers a named readiness check.
func (h *Health) Check(name string, fn func(context.Context) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.checks == nil {
		h.checks = map[string]func(context.Context) error{}
	}
	h.checks[name] = fn
}

// OpsRoutes adds /healthz, /readyz and /metrics to mux.
func OpsRoutes(mux *http.ServeMux, h *Health, m *Metrics) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		h.mu.Lock()
		checks := make(map[string]func(context.Context) error, len(h.checks))
		for k, v := range h.checks {
			checks[k] = v
		}
		h.mu.Unlock()
		results, healthy := map[string]string{}, true
		for name, fn := range checks {
			if err := fn(ctx); err != nil {
				results[name], healthy = err.Error(), false
			} else {
				results[name] = "ok"
			}
		}
		if healthy {
			_ = WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "checks": results})
			return
		}
		_ = WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "checks": results})
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}))
}

// Serve runs an HTTP server until ctx is cancelled, then stops accepting new
// connections and lets in-flight requests finish within drain (FR-016).
func Serve(ctx context.Context, log *slog.Logger, addr string, drain time.Duration, h http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down", "drain", drain.String())
	sctx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// HealthcheckFlag implements `<binary> -healthcheck` for container health
// checks in images without a shell: it probes the local /healthz and exits.
func HealthcheckFlag(addr string) {
	port := addr[strings.LastIndex(addr, ":")+1:]
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var body struct{ Status string }
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil || body.Status != "ok" {
		os.Exit(1)
	}
	os.Exit(0)
}
