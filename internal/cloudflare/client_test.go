package cloudflare

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchHTTPMetricsAggregatesByHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("expected bearer token auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"viewer": {
					"zones": [
						{
							"httpRequestsAdaptiveGroups": [
								{
									"dimensions": {
										"clientRequestHTTPHost": "example.com",
										"edgeResponseStatus": 200
									},
									"sum": {
										"requests": 10,
										"edgeResponseBytes": 1000,
										"cachedRequests": 5
									}
								},
								{
									"dimensions": {
										"clientRequestHTTPHost": "example.com",
										"edgeResponseStatus": 404
									},
									"sum": {
										"requests": 2,
										"edgeResponseBytes": 200,
										"cachedRequests": 0
									}
								},
								{
									"dimensions": {
										"clientRequestHTTPHost": "docs.example.com",
										"edgeResponseStatus": 301
									},
									"sum": {
										"requests": 7,
										"edgeResponseBytes": 700,
										"cachedRequests": 1
									}
								}
							]
						}
					]
				}
			}
		}`))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient("secret-token", 5*time.Second, Options{
		Endpoint:   srv.URL,
		HTTPClient: srv.Client(),
		Logger:     logger,
	})

	result, err := client.FetchHTTPMetrics(context.Background(), "zone-123", []string{"example.com", "docs.example.com"}, time.Now().Add(-time.Minute), time.Now())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	spendseer := result["example.com"]
	if spendseer.Requests != 12 {
		t.Fatalf("expected example.com requests to be 12, got %.0f", spendseer.Requests)
	}
	if spendseer.EdgeResponseBytes != 1200 {
		t.Fatalf("expected example.com bytes to be 1200, got %.0f", spendseer.EdgeResponseBytes)
	}
	if spendseer.StatusRequests["2xx"] != 10 {
		t.Fatalf("expected example.com 2xx to be 10, got %.0f", spendseer.StatusRequests["2xx"])
	}
	if spendseer.StatusRequests["4xx"] != 2 {
		t.Fatalf("expected example.com 4xx to be 2, got %.0f", spendseer.StatusRequests["4xx"])
	}

	docs := result["docs.example.com"]
	if docs.Requests != 7 {
		t.Fatalf("expected docs.example.com requests to be 7, got %.0f", docs.Requests)
	}
	if docs.StatusRequests["3xx"] != 7 {
		t.Fatalf("expected docs.example.com 3xx to be 7, got %.0f", docs.StatusRequests["3xx"])
	}
}

func TestFetchFirewallMetricsAggregatesByHostAction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"viewer": {
					"zones": [
						{
							"firewallEventsAdaptiveGroups": [
								{"dimensions": {"action": "block", "clientRequestHTTPHost": "example.com"}, "count": 2},
								{"dimensions": {"action": "managed_challenge", "clientRequestHTTPHost": "example.com"}, "count": 1},
								{"dimensions": {"action": "allow", "clientRequestHTTPHost": "docs.example.com"}, "count": 4}
							]
						}
					]
				}
			}
		}`))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient("secret-token", 5*time.Second, Options{Endpoint: srv.URL, HTTPClient: srv.Client(), Logger: logger})

	result, err := client.FetchFirewallMetrics(context.Background(), "zone-123", []string{"example.com", "docs.example.com"}, time.Now().Add(-time.Minute), time.Now())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result["example.com"]["block"] != 2 {
		t.Fatalf("expected block count for example.com to be 2, got %.0f", result["example.com"]["block"])
	}
	if result["example.com"]["managed_challenge"] != 1 {
		t.Fatalf("expected managed_challenge count for example.com to be 1, got %.0f", result["example.com"]["managed_challenge"])
	}
	if result["docs.example.com"]["allow"] != 4 {
		t.Fatalf("expected allow count for docs.example.com to be 4, got %.0f", result["docs.example.com"]["allow"])
	}
}

func TestFetchGatewayL7MetricsAggregatesByHostAction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"viewer": {
					"accounts": [
						{
							"gatewayL7RequestsAdaptiveGroups": [
								{"dimensions": {"action": "allow", "httpHost": "example.com"}, "count": 22},
								{"dimensions": {"action": "block", "httpHost": "templates.example.com"}, "count": 3}
							]
						}
					]
				}
			}
		}`))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient("secret-token", 5*time.Second, Options{Endpoint: srv.URL, HTTPClient: srv.Client(), Logger: logger})

	result, err := client.FetchGatewayL7Metrics(context.Background(), "account-123", []string{"example.com", "templates.example.com"}, time.Now().Add(-time.Minute), time.Now())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result["example.com"]["allow"] != 22 {
		t.Fatalf("expected allow count for example.com to be 22, got %.0f", result["example.com"]["allow"])
	}
	if result["templates.example.com"]["block"] != 3 {
		t.Fatalf("expected block count for templates.example.com to be 3, got %.0f", result["templates.example.com"]["block"])
	}
}

func TestFetchHTTPMetricsHandlesGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"permission denied"}]}`))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient("secret-token", 5*time.Second, Options{Endpoint: srv.URL, HTTPClient: srv.Client(), Logger: logger})

	_, err := client.FetchHTTPMetrics(context.Background(), "zone-123", []string{"example.com"}, time.Now().Add(-time.Minute), time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected graphql error to propagate, got %v", err)
	}
}
