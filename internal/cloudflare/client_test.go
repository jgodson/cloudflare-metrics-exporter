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
										"edgeResponseStatus": 200,
										"cacheStatus": "hit"
									},
									"count": 10,
									"sum": {
										"visits": 1,
										"edgeResponseBytes": 1000
									}
								},
								{
									"dimensions": {
										"clientRequestHTTPHost": "example.com",
										"edgeResponseStatus": 404,
										"cacheStatus": "dynamic"
									},
									"count": 2,
									"sum": {
										"visits": 1,
										"edgeResponseBytes": 200
									}
								},
								{
									"dimensions": {
										"clientRequestHTTPHost": "docs.example.com",
										"edgeResponseStatus": 301,
										"cacheStatus": "revalidated"
									},
									"count": 7,
									"sum": {
										"visits": 1,
										"edgeResponseBytes": 700
									}
								},
								{
									"dimensions": {
										"clientRequestHTTPHost": "wp-admin.example.com",
										"edgeResponseStatus": 404,
										"cacheStatus": "dynamic"
									},
									"count": 3,
									"sum": {
										"visits": 1,
										"edgeResponseBytes": 30
									}
								},
								{
									"dimensions": {
										"clientRequestHTTPHost": "example.com:8443",
										"edgeResponseStatus": 403,
										"cacheStatus": "dynamic"
									},
									"count": 1,
									"sum": {
										"visits": 1,
										"edgeResponseBytes": 10
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

	result, err := client.FetchHTTPMetrics(context.Background(), "zone-123", []string{"example.com", "docs.example.com", "idle.example.com"}, time.Now().Add(-time.Minute), time.Now())
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
	if spendseer.CachedRequests != 10 {
		t.Fatalf("expected example.com cached requests to be 10, got %.0f", spendseer.CachedRequests)
	}

	if spendseer.Visits != 2 {
		t.Fatalf("expected example.com visits to be 2, got %.0f", spendseer.Visits)
	}

	docs := result["docs.example.com"]
	if docs.Requests != 7 {
		t.Fatalf("expected docs.example.com requests to be 7, got %.0f", docs.Requests)
	}
	if docs.StatusRequests["3xx"] != 7 {
		t.Fatalf("expected docs.example.com 3xx to be 7, got %.0f", docs.StatusRequests["3xx"])
	}
	if docs.CachedRequests != 7 {
		t.Fatalf("expected docs.example.com cached requests to be 7, got %.0f", docs.CachedRequests)
	}

	if idle, ok := result["idle.example.com"]; !ok || idle.Requests != 0 {
		t.Fatalf("expected idle tracked host to be zero-filled, got %+v (present=%v)", idle, ok)
	}

	other := result[OtherHost]
	if other.Requests != 4 {
		t.Fatalf("expected untracked hosts to aggregate into other with 4 requests, got %.0f", other.Requests)
	}
	if other.StatusRequests["4xx"] != 4 {
		t.Fatalf("expected other 4xx to be 4, got %.0f", other.StatusRequests["4xx"])
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
								{"dimensions": {"action": "allow", "clientRequestHTTPHost": "docs.example.com"}, "count": 4},
								{"dimensions": {"action": "block", "clientRequestHTTPHost": "scanner.example.com"}, "count": 6}
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
	if result[OtherHost]["block"] != 6 {
		t.Fatalf("expected untracked block events in other to be 6, got %.0f", result[OtherHost]["block"])
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

func TestListZonesPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("status"); got != "active" {
			t.Fatalf("expected status=active filter, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"z1","name":"Example.com"}],"result_info":{"total_pages":2}}`))
		case "2":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"z2","name":"example.org"}],"result_info":{"total_pages":2}}`))
		default:
			t.Fatalf("unexpected page %q", r.URL.Query().Get("page"))
		}
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient("secret-token", 5*time.Second, Options{RESTEndpoint: srv.URL, HTTPClient: srv.Client(), Logger: logger})

	zones, err := client.ListZones(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(zones) != 2 || zones[0] != (Zone{ID: "z1", Name: "example.com"}) || zones[1] != (Zone{ID: "z2", Name: "example.org"}) {
		t.Fatalf("unexpected zones: %+v", zones)
	}
}

func TestListZonesReturnsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"Unauthorized to access requested resource"}]}`))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient("secret-token", 5*time.Second, Options{RESTEndpoint: srv.URL, HTTPClient: srv.Client(), Logger: logger})

	if _, err := client.ListZones(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected 403 error, got %v", err)
	}
}
