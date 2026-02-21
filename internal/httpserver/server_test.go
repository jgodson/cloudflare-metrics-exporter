package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasongodson/cloudflare-metrics-exporter/internal/metrics"
)

func TestHealthAndReadinessHandlers(t *testing.T) {
	ready := false
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := metrics.NewRegistry()
	server := New(":8080", registry.Gatherer(), func() bool { return ready }, logger)

	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(healthResp, healthReq)
	if healthResp.Code != http.StatusOK {
		t.Fatalf("expected healthz 200, got %d", healthResp.Code)
	}

	readyReq := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readyResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(readyResp, readyReq)
	if readyResp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readyz 503 when not ready, got %d", readyResp.Code)
	}

	ready = true
	readyResp = httptest.NewRecorder()
	server.Handler().ServeHTTP(readyResp, readyReq)
	if readyResp.Code != http.StatusOK {
		t.Fatalf("expected readyz 200 when ready, got %d", readyResp.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := metrics.NewRegistry()
	server := New(":8080", registry.Gatherer(), func() bool { return true }, logger)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected metrics endpoint 200, got %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "cf_exporter_up") {
		t.Fatalf("expected metrics output to include cf_exporter_up")
	}
}
