package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
)

// AC-29: an unknown adapter name, like any missing or malformed variable,
// stops startup with a message naming the variable (AR-009, DR-007).
func TestEnv_ReportsEveryProblemByName(t *testing.T) {
	t.Setenv("CCSRA_ADAPTER_STORAGE", "azblob")
	t.Setenv("CCSRA_MAX_PAGES", "many")
	var e Env
	e.Adapter("CCSRA_ADAPTER_STORAGE", "s3")
	e.Required("CCSRA_DEFINITELY_UNSET")
	e.Int("CCSRA_MAX_PAGES", 50)
	err := e.Err()
	if err == nil {
		t.Fatal("no error")
	}
	for _, name := range []string{"CCSRA_ADAPTER_STORAGE", "CCSRA_DEFINITELY_UNSET", "CCSRA_MAX_PAGES"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %s: %v", name, err)
		}
	}
}

// AC-30: the resolved adapter set is exposed on /metrics.
func TestReportAdapters_ExposesGauge(t *testing.T) {
	m := NewMetrics()
	m.ReportAdapters(slog.New(slog.NewTextHandler(io.Discard, nil)), "v1", "local", map[string]string{"storage": "s3"})
	mux := http.NewServeMux()
	OpsRoutes(mux, &Health{}, m)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `ccsra_adapter_info{adapter="s3",port="storage"} 1`) {
		t.Error("adapter gauge missing from /metrics")
	}
}

func TestReadyz_ReportsFailingChecks(t *testing.T) {
	var h Health
	h.Check("database", func(context.Context) error { return nil })
	h.Check("broker", func(context.Context) error { return errors.New("connection refused") })
	mux := http.NewServeMux()
	OpsRoutes(mux, &h, NewMetrics())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable || body.Status != "degraded" || body.Checks["database"] != "ok" || body.Checks["broker"] == "ok" {
		t.Errorf("got %d %+v", rec.Code, body)
	}
}

func TestWriteError_MapsDomainCodes(t *testing.T) {
	for code, status := range map[string]int{
		domain.CodeInvalidStateTransition: http.StatusConflict,
		domain.CodeCaseNotFound:           http.StatusNotFound,
		domain.CodeDecisionReasonRequired: http.StatusUnprocessableEntity,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil).WithContext(WithCorrelationID(t.Context(), "c-1"))
		WriteError(rec, req, slog.Default(), &domain.Error{Code: code, Detail: "x"})
		var body problemBody
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != status || body.Code != code || body.CorrelationID != "c-1" {
			t.Errorf("%s: got %d %+v", code, rec.Code, body)
		}
	}
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest("GET", "/", nil), slog.New(slog.NewTextHandler(io.Discard, nil)), errors.New("db password is hunter2"))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("internal errors must stay generic: %d %s", rec.Code, rec.Body)
	}
}
