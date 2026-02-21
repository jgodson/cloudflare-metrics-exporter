package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jasongodson/cloudflare-metrics-exporter/internal/cloudflare"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/config"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeClient struct {
	httpResultByZone     map[string]map[string]cloudflare.HostMetrics
	httpErrByZone        map[string]error
	firewallResultByZone map[string]map[string]map[string]float64
	firewallErrByZone    map[string]error
	gatewayResult        map[string]map[string]float64
	gatewayErr           error
	httpCalls            int
	firewallCalls        int
	gatewayCalls         int
}

func (f *fakeClient) FetchHTTPMetrics(ctx context.Context, zoneTag string, hosts []string, from, to time.Time) (map[string]cloudflare.HostMetrics, error) {
	f.httpCalls++
	if err, ok := f.httpErrByZone[zoneTag]; ok && err != nil {
		return nil, err
	}
	if result, ok := f.httpResultByZone[zoneTag]; ok {
		return result, nil
	}
	return map[string]cloudflare.HostMetrics{}, nil
}

func (f *fakeClient) FetchFirewallMetrics(ctx context.Context, zoneTag string, hosts []string, from, to time.Time) (map[string]map[string]float64, error) {
	f.firewallCalls++
	if err, ok := f.firewallErrByZone[zoneTag]; ok && err != nil {
		return nil, err
	}
	if result, ok := f.firewallResultByZone[zoneTag]; ok {
		return result, nil
	}
	return map[string]map[string]float64{}, nil
}

func (f *fakeClient) FetchGatewayL7Metrics(ctx context.Context, accountTag string, hosts []string, from, to time.Time) (map[string]map[string]float64, error) {
	f.gatewayCalls++
	if f.gatewayErr != nil {
		return nil, f.gatewayErr
	}
	return f.gatewayResult, nil
}

func TestPollOnceSuccessUpdatesMetrics(t *testing.T) {
	cfg := config.Config{
		ZoneTags:       []string{"zone-123", "zone-456"},
		AccountTag:     "account-123",
		Hosts:          []string{"example.com"},
		Window:         time.Minute,
		PollInterval:   time.Minute,
		RequestTimeout: 2 * time.Second,
	}

	client := &fakeClient{
		httpResultByZone: map[string]map[string]cloudflare.HostMetrics{
			"zone-123": {
				"example.com": {
					Requests:          21,
					EdgeResponseBytes: 2100,
					CachedRequests:    3,
					StatusRequests: map[string]float64{
						"2xx": 17,
						"4xx": 4,
					},
				},
			},
			"zone-456": {
				"example.com": {
					Requests:          5,
					EdgeResponseBytes: 500,
					CachedRequests:    1,
					StatusRequests: map[string]float64{
						"2xx": 5,
					},
				},
			},
		},
		firewallResultByZone: map[string]map[string]map[string]float64{
			"zone-123": {
				"example.com": {
					"block": 2,
				},
			},
			"zone-456": {
				"example.com": {
					"allow": 5,
				},
			},
		},
		gatewayResult: map[string]map[string]float64{
			"example.com": {
				"allow": 19,
			},
		},
	}

	registry := metrics.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := New(cfg, client, registry, logger)
	poller.maxAttempts = 1
	poller.now = func() time.Time {
		return time.Unix(1700000000, 0).UTC()
	}

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll success, got %v", err)
	}

	if testutil.ToFloat64(registry.ExporterUp) != 1 {
		t.Fatalf("expected exporter up to be 1")
	}

	if testutil.ToFloat64(registry.HTTPRequestsWindow.WithLabelValues("zone-123", "example.com")) != 21 {
		t.Fatalf("expected zone-123 request gauge to be 21")
	}
	if testutil.ToFloat64(registry.HTTPRequestsWindow.WithLabelValues("zone-456", "example.com")) != 5 {
		t.Fatalf("expected zone-456 request gauge to be 5")
	}

	if testutil.ToFloat64(registry.FirewallEventsWindow.WithLabelValues("zone-123", "example.com", "block")) != 2 {
		t.Fatalf("expected zone-123 firewall gauge to be 2")
	}
	if testutil.ToFloat64(registry.GatewayL7RequestsWindow.WithLabelValues("account-123", "example.com", "allow")) != 19 {
		t.Fatalf("expected gateway gauge to be 19")
	}

	for _, zone := range cfg.ZoneTags {
		if testutil.ToFloat64(registry.QueryUp.WithLabelValues("http", zone)) != 1 {
			t.Fatalf("expected http query up for zone %s", zone)
		}
		if testutil.ToFloat64(registry.QueryUp.WithLabelValues("firewall", zone)) != 1 {
			t.Fatalf("expected firewall query up for zone %s", zone)
		}
	}
	if testutil.ToFloat64(registry.QueryUp.WithLabelValues("gateway_l7", cfg.AccountTag)) != 1 {
		t.Fatalf("expected gateway query up for account")
	}
}

func TestQueryFailureDoesNotMarkExporterDown(t *testing.T) {
	cfg := config.Config{
		ZoneTags:       []string{"zone-123", "zone-456"},
		Hosts:          []string{"example.com"},
		Window:         time.Minute,
		PollInterval:   time.Minute,
		RequestTimeout: 2 * time.Second,
	}

	client := &fakeClient{
		httpErrByZone: map[string]error{
			"zone-123": errors.New("cloudflare unavailable"),
		},
		httpResultByZone: map[string]map[string]cloudflare.HostMetrics{
			"zone-456": {
				"example.com": {Requests: 8, StatusRequests: map[string]float64{}},
			},
		},
		firewallResultByZone: map[string]map[string]map[string]float64{
			"zone-123": {"example.com": {"allow": 1}},
			"zone-456": {"example.com": {"allow": 5}},
		},
	}
	registry := metrics.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := New(cfg, client, registry, logger)
	poller.maxAttempts = 1

	err := poller.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("expected poll cycle to continue despite query failure, got %v", err)
	}

	if testutil.ToFloat64(registry.ExporterUp) != 1 {
		t.Fatalf("expected exporter up to remain 1")
	}
	if testutil.ToFloat64(registry.QueryUp.WithLabelValues("http", "zone-123")) != 0 {
		t.Fatalf("expected failed zone http query up to be 0")
	}
	if testutil.ToFloat64(registry.QueryUp.WithLabelValues("http", "zone-456")) != 1 {
		t.Fatalf("expected successful zone http query up to be 1")
	}
	if testutil.ToFloat64(registry.QueryErrorsTotal.WithLabelValues("http", "zone-123")) != 1 {
		t.Fatalf("expected zone-123 http query errors to be 1")
	}
	if testutil.ToFloat64(registry.ExporterPollErrors) != 1 {
		t.Fatalf("expected poll errors to increment")
	}
}

func TestPollOnceSkipsGatewayWhenAccountTagMissing(t *testing.T) {
	cfg := config.Config{
		ZoneTags:       []string{"zone-123"},
		Hosts:          []string{"example.com"},
		Window:         time.Minute,
		PollInterval:   time.Minute,
		RequestTimeout: 2 * time.Second,
	}

	client := &fakeClient{
		httpResultByZone: map[string]map[string]cloudflare.HostMetrics{
			"zone-123": {
				"example.com": {Requests: 5, StatusRequests: map[string]float64{}},
			},
		},
		firewallResultByZone: map[string]map[string]map[string]float64{
			"zone-123": {
				"example.com": {"allow": 5},
			},
		},
		gatewayErr: errors.New("should not be called"),
	}

	registry := metrics.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := New(cfg, client, registry, logger)
	poller.maxAttempts = 1

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll success, got %v", err)
	}

	if count := testutil.CollectAndCount(registry.GatewayL7RequestsWindow); count != 0 {
		t.Fatalf("expected no gateway series when account tag missing, got %d", count)
	}
}

func TestPollOnceSkipsDisabledQueries(t *testing.T) {
	cfg := config.Config{
		ZoneTags:       []string{"zone-123"},
		AccountTag:     "account-123",
		Hosts:          []string{"example.com"},
		Window:         time.Minute,
		PollInterval:   time.Minute,
		RequestTimeout: 2 * time.Second,
		EnabledQueries: map[string]bool{
			queryHTTP: true,
		},
	}

	client := &fakeClient{
		httpResultByZone: map[string]map[string]cloudflare.HostMetrics{
			"zone-123": {
				"example.com": {Requests: 5, StatusRequests: map[string]float64{}},
			},
		},
		firewallErrByZone: map[string]error{
			"zone-123": errors.New("should not be called"),
		},
		gatewayErr: errors.New("should not be called"),
	}

	registry := metrics.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := New(cfg, client, registry, logger)
	poller.maxAttempts = 1

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll success, got %v", err)
	}

	if client.httpCalls != 1 {
		t.Fatalf("expected one http call, got %d", client.httpCalls)
	}
	if client.firewallCalls != 0 {
		t.Fatalf("expected firewall to be skipped, got %d calls", client.firewallCalls)
	}
	if client.gatewayCalls != 0 {
		t.Fatalf("expected gateway to be skipped, got %d calls", client.gatewayCalls)
	}
	if count := testutil.CollectAndCount(registry.QueryUp); count != 1 {
		t.Fatalf("expected only one query_up series (http), got %d", count)
	}
}
