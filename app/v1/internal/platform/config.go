package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Env reads configuration from environment variables and collects every
// problem, so that startup can fail with one message naming each offending
// variable (FR-015, DR-007).
type Env struct {
	problems []string
}

func (e *Env) lookup(name string) (string, bool) {
	v, ok := os.LookupEnv(name)
	return strings.TrimSpace(v), ok && strings.TrimSpace(v) != ""
}

// String returns the variable or def.
func (e *Env) String(name, def string) string {
	if v, ok := e.lookup(name); ok {
		return v
	}
	return def
}

// Required returns the variable and records a problem if it is unset.
func (e *Env) Required(name string) string {
	v, ok := e.lookup(name)
	if !ok {
		e.problems = append(e.problems, name+" is required")
	}
	return v
}

// Int returns the variable as an integer, or def.
func (e *Env) Int(name string, def int) int {
	v, ok := e.lookup(name)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.problems = append(e.problems, fmt.Sprintf("%s must be an integer, got %q", name, v))
	}
	return n
}

// Seconds returns a *_SECONDS variable as a duration, or def seconds.
func (e *Env) Seconds(name string, def int) time.Duration {
	return time.Duration(e.Int(name, def)) * time.Second
}

// Bool returns the variable as a boolean, or def.
func (e *Env) Bool(name string, def bool) bool {
	v, ok := e.lookup(name)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.problems = append(e.problems, fmt.Sprintf("%s must be true or false, got %q", name, v))
	}
	return b
}

// List returns a comma-separated variable, or def.
func (e *Env) List(name string, def ...string) []string {
	v, ok := e.lookup(name)
	if !ok {
		return def
	}
	var out []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// Adapter returns a required CCSRA_ADAPTER_* variable. An unknown name is a
// startup error, never a silent default (AR-009).
func (e *Env) Adapter(name string, known ...string) string {
	v := e.Required(name)
	if v != "" && !slices.Contains(known, v) {
		e.problems = append(e.problems, fmt.Sprintf("%s=%q is not a known adapter (known: %s)", name, v, strings.Join(known, ", ")))
	}
	return v
}

// Secret resolves a secret through the configured SecretProvider and records
// a problem if it cannot be read.
func (e *Env) Secret(ctx context.Context, p interface {
	Get(context.Context, string) (string, error)
}, name string) string {
	v, err := p.Get(ctx, name)
	if err != nil {
		e.problems = append(e.problems, fmt.Sprintf("%s: %v", name, err))
	}
	return v
}

// Problem records a configuration problem found outside the readers above.
func (e *Env) Problem(format string, args ...any) {
	e.problems = append(e.problems, fmt.Sprintf(format, args...))
}

// Err returns all collected problems, or nil.
func (e *Env) Err() error {
	if len(e.problems) == 0 {
		return nil
	}
	return errors.New("invalid configuration: " + strings.Join(e.problems, "; "))
}

// Common holds the variables every service reads (SRS §9.3).
type Common struct {
	Service      string
	Variant      string
	Target       string
	HTTPAddr     string
	LogLevel     string
	DrainTimeout time.Duration
	OTLPEndpoint string
}

// ReadCommon reads the "all services" block of SRS §9.3.
func ReadCommon(e *Env, service string) Common {
	return Common{
		Service:      e.String("CCSRA_SERVICE_NAME", service),
		Variant:      e.Required("CCSRA_VARIANT"),
		Target:       e.Required("CCSRA_TARGET"),
		HTTPAddr:     e.String("CCSRA_HTTP_ADDR", ":8080"),
		LogLevel:     e.String("CCSRA_LOG_LEVEL", "info"),
		DrainTimeout: e.Seconds("CCSRA_SHUTDOWN_DRAIN_SECONDS", 30),
		OTLPEndpoint: e.String("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
	}
}
