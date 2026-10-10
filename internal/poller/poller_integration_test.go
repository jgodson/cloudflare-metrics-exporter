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
		case r.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zone_123","name":"example.com"}],"result_info":{"total_pages":1}}`))
		case strings.Contains(queryBody, "httpRequestsAdaptiveGroups"):
			_, _ = w.Write([]byte(`{
				"data": {
					"viewer": {
						"zones": [
							{
								"httpRequestsAdaptiveGroups": [
									{
										"dimensions": {"clientRequestHTTPHost": "example.com", "edgeResponseStatus": 200, "cacheStatus": "hit"},
										"count": 15, "sum": {"visits": 1, "edgeResponseBytes": 1500}
									},
									{
										"dimensions": {"clientRequestHTTPHost": "docs.example.com", "edgeResponseStatus": 404, "cacheStatus": "dynamic"},
										"count": 2, "sum": {"visits": 1, "edgeResponseBytes": 200}
									},
									{
										"dimensions": {"clientRequestHTTPHost": "templates.example.com", "edgeResponseStatus": 301, "cacheStatus": "stale"},
										"count": 4, "sum": {"visits": 1, "edgeResponseBytes": 400}
									},
									{
										"dimensions": {"clientRequestHTTPHost": "wordpress.example.com", "edgeResponseStatus": 404, "cacheStatus": "dynamic"},
										"count": 6, "sum": {"visits": 1, "edgeResponseBytes": 60}
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
		Hosts:          []string{"docs.example.com", "templates.example.com"},
		PollInterval:   time.Minute,
		ZoneRefresh:    10 * time.Minute,
		Window:         time.Minute,
		HTTPAddr:       ":8080",
		RequestTimeout: 5 * time.Second,
		LogLevel:       "info",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := metrics.NewRegistry()
	client := cloudflare.NewClient(cfg.APIToken, cfg.RequestTimeout, cloudflare.Options{
		Endpoint:     mockCF.URL + "/graphql",
		RESTEndpoint: mockCF.URL,
		HTTPClient:   mockCF.Client(),
		Logger:       logger,
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
	for _, want := range []string{
		`cf_http_requests_window{host="example.com",zone="zone_123",zone_name="example.com"} 15`,
		`cf_http_requests_window{host="docs.example.com",zone="zone_123",zone_name="example.com"} 2`,
		`cf_http_requests_window{host="templates.example.com",zone="zone_123",zone_name="example.com"} 4`,
		`cf_http_requests_window{host="other",zone="zone_123",zone_name="example.com"} 6`,
		`cf_http_visits_window{host="example.com",zone="zone_123",zone_name="example.com"} 1`,
		`cf_exporter_query_up{query="zones",target="discovery"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in metrics output:\n%s", want, body)
		}
	}
}
