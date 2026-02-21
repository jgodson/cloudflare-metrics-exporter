package poller_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasongodson/cloudflare-metrics-exporter/internal/cloudflare"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/config"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/httpserver"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/metrics"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/poller"
)

func TestPollerAndMetricsEndpointIntegration(t *testing.T) {
	mockCF := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		queryBody := string(body)
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(queryBody, "httpRequestsAdaptiveGroups"):
			_, _ = w.Write([]byte(`{
				"data": {
					"viewer": {
						"zones": [
							{
								"httpRequestsAdaptiveGroups": [
									{
										"dimensions": {"clientRequestHTTPHost": "example.com", "edgeResponseStatus": 200},
										"sum": {"visits": 15, "edgeResponseBytes": 1500, "cachedRequests": 6}
									},
									{
										"dimensions": {"clientRequestHTTPHost": "docs.example.com", "edgeResponseStatus": 404},
										"sum": {"visits": 2, "edgeResponseBytes": 200, "cachedRequests": 0}
									},
									{
										"dimensions": {"clientRequestHTTPHost": "templates.example.com", "edgeResponseStatus": 301},
										"sum": {"visits": 4, "edgeResponseBytes": 400, "cachedRequests": 1}
									}
								]
							}
						]
					}
				}
			}`))
		case strings.Contains(queryBody, "firewallEventsAdaptiveGroups"):
			_, _ = w.Write([]byte(`{
				"data": {
					"viewer": {
						"zones": [
							{
								"firewallEventsAdaptiveGroups": [
									{
										"dimensions": {"action": "block", "clientRequestHTTPHost": "example.com"},
										"count": 3
									},
									{
										"dimensions": {"action": "managed_challenge", "clientRequestHTTPHost": "docs.example.com"},
										"count": 1
									}
								]
							}
						]
					}
				}
			}`))
		case strings.Contains(queryBody, "gatewayL7RequestsAdaptiveGroups"):
			_, _ = w.Write([]byte(`{
				"data": {
					"viewer": {
						"accounts": [
							{
								"gatewayL7RequestsAdaptiveGroups": [
									{
										"dimensions": {"action": "allow", "httpHost": "example.com"},
										"count": 12
									},
									{
										"dimensions": {"action": "block", "httpHost": "templates.example.com"},
										"count": 2
									}
								]
							}
						]
					}
				}
			}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected query"}]}`))
		}
	}))
	defer mockCF.Close()

	cfg := config.Config{
		APIToken:       "token",
		ZoneTags:       []string{"zone_123"},
		AccountTag:     "account_123",
		Hosts:          []string{"example.com", "docs.example.com", "templates.example.com"},
		PollInterval:   time.Minute,
		Window:         time.Minute,
		HTTPAddr:       ":8080",
		RequestTimeout: 5 * time.Second,
		LogLevel:       "info",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := metrics.NewRegistry()
	client := cloudflare.NewClient(cfg.APIToken, cfg.RequestTimeout, cloudflare.Options{
		Endpoint:   mockCF.URL,
		HTTPClient: mockCF.Client(),
		Logger:     logger,
	})

	p := poller.New(cfg, client, registry, logger)
	if err := p.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll to succeed, got %v", err)
	}

	srv := httpserver.New(":8080", registry.Gatherer(), func() bool { return true }, logger)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected /metrics 200, got %d", resp.Code)
	}

	body := resp.Body.String()
	for _, metricName := range []string{"cf_http_requests_window", "cf_firewall_events_window", "cf_gateway_l7_requests_window"} {
		if !strings.Contains(body, metricName) {
			t.Fatalf("expected %s in metrics body", metricName)
		}
	}
	for _, host := range cfg.Hosts {
		if !strings.Contains(body, host) {
			t.Fatalf("expected host %s in metrics output", host)
		}
	}
}
