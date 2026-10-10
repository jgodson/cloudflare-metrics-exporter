package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jasongodson/cloudflare-metrics-exporter/internal/cloudflare"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/config"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeClient struct {
	zones                []cloudflare.Zone
	zonesErr             error
	zonesCalls           int
	httpHostsByZone      map[string][]string
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

func (f *fakeClient) ListZones(ctx context.Context) ([]cloudflare.Zone, error) {
	f.zonesCalls++
	if f.zonesErr != nil {
		return nil, f.zonesErr
	}
	return f.zones, nil
}

func (f *fakeClient) FetchHTTPMetrics(ctx context.Context, zoneTag string, hosts []string, from, to time.Time) (map[string]cloudflare.HostMetrics, error) {
	f.httpCalls++
	if f.httpHostsByZone == nil {
		f.httpHostsByZone = map[string][]string{}
	}
	f.httpHostsByZone[zoneTag] = hosts
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

var testZones = []cloudflare.Zone{
	{ID: "zone-123", Name: "example.com"},
	{ID: "zone-456", Name: "example.org"},
}

func newTestPoller(cfg config.Config, client *fakeClient) (*Poller, *metrics.Registry) {
	if cfg.Window == 0 {
		cfg.Window = time.Minute
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Minute
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 2 * time.Second
	}
	if cfg.ZoneRefresh == 0 {
		cfg.ZoneRefresh = 10 * time.Minute
	}
	registry := metrics.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(cfg, client, registry, logger)
	p.maxAttempts = 1
	p.sleep = func(context.Context, time.Duration) bool { return true }
	p.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	return p, registry
}

func TestPollOnceSuccessUpdatesMetrics(t *testing.T) {
	cfg := config.Config{
		AccountTag: "account-123",
		Hosts:      []string{"docs.example.com", "www.example.org"},
	}

	client := &fakeClient{
		zones: testZones,
		httpResultByZone: map[string]map[string]cloudflare.HostMetrics{
			"zone-123": {
				"example.com":        {Requests: 21, EdgeResponseBytes: 2100, CachedRequests: 3, StatusRequests: map[string]float64{"2xx": 17, "4xx": 4}},
				"docs.example.com":   {Requests: 2, StatusRequests: map[string]float64{"2xx": 2}},
				cloudflare.OtherHost: {Requests: 9, StatusRequests: map[string]float64{"4xx": 9}},
			},
			"zone-456": {
				"example.org":     {Requests: 5, EdgeResponseBytes: 500, CachedRequests: 1, StatusRequests: map[string]float64{"2xx": 5}},
				"www.example.org": {StatusRequests: map[string]float64{}},
			},
		},
		firewallResultByZone: map[string]map[string]map[string]float64{
			"zone-123": {"example.com": {"block": 2}},
		},
		gatewayResult: map[string]map[string]float64{
			"example.com": {"allow": 19},
		},
	}

	poller, registry := newTestPoller(cfg, client)
	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll success, got %v", err)
	}

	if testutil.ToFloat64(registry.ExporterUp) != 1 {
		t.Fatalf("expected exporter up to be 1")
	}

	// Apex is tracked automatically and configured hosts are assigned to their own zone only.
	if got := client.httpHostsByZone["zone-123"]; strings.Join(got, ",") != "example.com,docs.example.com" {
		t.Fatalf("unexpected tracked hosts for zone-123: %v", got)
	}
	if got := client.httpHostsByZone["zone-456"]; strings.Join(got, ",") != "example.org,www.example.org" {
		t.Fatalf("unexpected tracked hosts for zone-456: %v", got)
	}

	if testutil.ToFloat64(registry.HTTPRequestsWindow.WithLabelValues("zone-123", "example.com", "example.com")) != 21 {
		t.Fatalf("expected example.com request gauge to be 21")
	}
	if testutil.ToFloat64(registry.HTTPRequestsWindow.WithLabelValues("zone-123", "example.com", cloudflare.OtherHost)) != 9 {
		t.Fatalf("expected other request gauge to be 9")
	}
	if testutil.ToFloat64(registry.HTTPRequestsWindow.WithLabelValues("zone-456", "example.org", "example.org")) != 5 {
		t.Fatalf("expected example.org request gauge to be 5")
	}
	// 3 hosts in zone-123 + 2 hosts in zone-456; no cross-zone series.
	if count := testutil.CollectAndCount(registry.HTTPRequestsWindow); count != 5 {
		t.Fatalf("expected 5 request series, got %d", count)
	}

	if testutil.ToFloat64(registry.FirewallEventsWindow.WithLabelValues("zone-123", "example.com", "example.com", "block")) != 2 {
		t.Fatalf("expected zone-123 firewall gauge to be 2")
	}
	if testutil.ToFloat64(registry.GatewayL7RequestsWindow.WithLabelValues("account-123", "example.com", "allow")) != 19 {
		t.Fatalf("expected gateway gauge to be 19")
	}

	for _, zone := range testZones {
		if testutil.ToFloat64(registry.QueryUp.WithLabelValues("http", zone.ID)) != 1 {
			t.Fatalf("expected http query up for zone %s", zone.ID)
		}
		if testutil.ToFloat64(registry.QueryUp.WithLabelValues("firewall", zone.ID)) != 1 {
			t.Fatalf("expected firewall query up for zone %s", zone.ID)
		}
	}
	if testutil.ToFloat64(registry.QueryUp.WithLabelValues("zones", "discovery")) != 1 {
		t.Fatalf("expected zone discovery query up")
	}
	if testutil.ToFloat64(registry.QueryUp.WithLabelValues("gateway_l7", cfg.AccountTag)) != 1 {
		t.Fatalf("expected gateway query up for account")
	}
}

func TestBuildZoneTargets(t *testing.T) {
	zones := []cloudflare.Zone{
		{ID: "z-org", Name: "example.org"},
		{ID: "z-com", Name: "example.com"},
		{ID: "z-sub", Name: "eu.example.com"},
	}
	hosts := []string{"example.com", "app.example.com", "api.eu.example.com", "unknown.net", "notexample.com"}

	targets, unmatched, missing := buildZoneTargets(zones, nil, hosts)
	if len(missing) != 0 {
		t.Fatalf("expected no missing tags, got %v", missing)
	}
	if strings.Join(unmatched, ",") != "unknown.net,notexample.com" {
		t.Fatalf("unexpected unmatched hosts: %v", unmatched)
	}

	got := map[string]string{}
	for _, target := range targets {
		got[target.Name] = strings.Join(target.Hosts, ",")
	}
	want := map[string]string{
		"example.com":    "example.com,app.example.com",
		"eu.example.com": "eu.example.com,api.eu.example.com",
		"example.org":    "example.org",
	}
	for name, hosts := range want {
		if got[name] != hosts {
			t.Fatalf("zone %s: expected hosts %q, got %q", name, hosts, got[name])
		}
	}

	targets, _, missing = buildZoneTargets(zones, []string{"z-com", "z-missing"}, hosts)
	if len(targets) != 1 || targets[0].Tag != "z-com" {
		t.Fatalf("expected zone tag filter to keep only z-com, got %+v", targets)
	}
	if strings.Join(missing, ",") != "z-missing" {
		t.Fatalf("expected z-missing to be reported, got %v", missing)
	}
}

func TestZoneDiscoveryRefreshesOnInterval(t *testing.T) {
	client := &fakeClient{zones: testZones[:1]}
	poller, registry := newTestPoller(config.Config{EnabledQueries: map[string]bool{queryHTTP: true}}, client)

	now := time.Unix(1700000000, 0).UTC()
	poller.now = func() time.Time { return now }

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll success, got %v", err)
	}
	now = now.Add(5 * time.Minute)
	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll success, got %v", err)
	}
	if client.zonesCalls != 1 {
		t.Fatalf("expected zones to be cached within refresh interval, got %d calls", client.zonesCalls)
	}

	// A new zone appears and the old one disappears.
	client.zones = testZones[1:]
	now = now.Add(6 * time.Minute)
	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll success, got %v", err)
	}
	if client.zonesCalls != 2 {
		t.Fatalf("expected zones to refresh after interval, got %d calls", client.zonesCalls)
	}
	if testutil.ToFloat64(registry.HTTPRequestsWindow.WithLabelValues("zone-456", "example.org", "example.org")) != 0 {
		t.Fatalf("expected new zone series")
	}
	if count := testutil.CollectAndCount(registry.QueryUp); count != 2 {
		t.Fatalf("expected removed zone query_up series to be deleted (zones + http), got %d", count)
	}
}

func TestZoneDiscoveryFailureFallsBackToZoneTags(t *testing.T) {
	cfg := config.Config{
		ZoneTags:       []string{"zone-123"},
		Hosts:          []string{"example.com"},
		EnabledQueries: map[string]bool{queryHTTP: true},
	}
	client := &fakeClient{
		zonesErr: errors.New("forbidden"),
		httpResultByZone: map[string]map[string]cloudflare.HostMetrics{
			"zone-123": {"example.com": {Requests: 4, StatusRequests: map[string]float64{}}},
		},
	}
	poller, registry := newTestPoller(cfg, client)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll cycle to continue, got %v", err)
	}
	if testutil.ToFloat64(registry.QueryUp.WithLabelValues("zones", "discovery")) != 0 {
		t.Fatalf("expected zone discovery query down")
	}
	if testutil.ToFloat64(registry.HTTPRequestsWindow.WithLabelValues("zone-123", "", "example.com")) != 4 {
		t.Fatalf("expected fallback zone to be polled")
	}
	if testutil.ToFloat64(registry.ExporterPollErrors) != 1 {
		t.Fatalf("expected poll errors to increment")
	}

	// Discovery is retried on the next poll rather than waiting for the refresh interval.
	client.zonesErr = nil
	client.zones = testZones[:1]
	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll success, got %v", err)
	}
	if client.zonesCalls != 2 {
		t.Fatalf("expected discovery retry, got %d calls", client.zonesCalls)
	}
	if testutil.ToFloat64(registry.HTTPRequestsWindow.WithLabelValues("zone-123", "example.com", "example.com")) != 4 {
		t.Fatalf("expected discovered zone name label after recovery")
	}
}

func TestQueryFailureDoesNotMarkExporterDown(t *testing.T) {
	client := &fakeClient{
		zones: testZones,
		httpErrByZone: map[string]error{
			"zone-123": errors.New("cloudflare unavailable"),
		},
	}
	poller, registry := newTestPoller(config.Config{}, client)

	if err := poller.PollOnce(context.Background()); err != nil {
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
	client := &fakeClient{
		zones:      testZones,
		gatewayErr: errors.New("should not be called"),
	}
	poller, registry := newTestPoller(config.Config{}, client)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("expected poll success, got %v", err)
	}
	if client.gatewayCalls != 0 {
		t.Fatalf("expected gateway to be skipped, got %d calls", client.gatewayCalls)
	}
	if count := testutil.CollectAndCount(registry.GatewayL7RequestsWindow); count != 0 {
		t.Fatalf("expected no gateway series when account tag missing, got %d", count)
	}
}

func TestPollOnceSkipsDisabledQueries(t *testing.T) {
	cfg := config.Config{
		AccountTag:     "account-123",
		EnabledQueries: map[string]bool{queryHTTP: true},
	}
	client := &fakeClient{
		zones: testZones[:1],
		firewallErrByZone: map[string]error{
			"zone-123": errors.New("should not be called"),
		},
		gatewayErr: errors.New("should not be called"),
	}
	poller, registry := newTestPoller(cfg, client)

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
	// zones discovery + http for one zone.
	if count := testutil.CollectAndCount(registry.QueryUp); count != 2 {
		t.Fatalf("expected two query_up series (zones, http), got %d", count)
	}
}
