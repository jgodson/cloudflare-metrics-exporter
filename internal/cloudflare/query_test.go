package cloudflare

import (
	"strings"
	"testing"
	"time"
)

func TestBuildQueryVariables(t *testing.T) {
	from := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)

	vars := BuildQueryVariables("zone_123", []string{"example.com"}, from, to)
	if vars.ZoneTag != "zone_123" {
		t.Fatalf("expected zone tag to be set")
	}
	if len(vars.Hosts) != 1 || vars.Hosts[0] != "example.com" {
		t.Fatalf("expected hosts to be preserved")
	}
	if vars.From != "2026-02-20T10:00:00Z" {
		t.Fatalf("unexpected from value: %s", vars.From)
	}
	if vars.To != "2026-02-20T10:01:00Z" {
		t.Fatalf("unexpected to value: %s", vars.To)
	}
}

func TestBuildAccountQueryVariables(t *testing.T) {
	from := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)

	vars := BuildAccountQueryVariables("account_123", []string{"example.com"}, from, to)
	if vars.AccountTag != "account_123" {
		t.Fatalf("expected account tag to be set")
	}
	if vars.From != "2026-02-20T10:00:00Z" {
		t.Fatalf("unexpected from value: %s", vars.From)
	}
	if vars.To != "2026-02-20T10:01:00Z" {
		t.Fatalf("unexpected to value: %s", vars.To)
	}
}

func TestHTTPMetricsQueryContainsExpectedFields(t *testing.T) {
	required := []string{
		"httpRequestsAdaptiveGroups",
		"clientRequestHTTPHost",
		"edgeResponseStatus",
		"visits",
		"edgeResponseBytes",
		"cachedRequests",
	}
	for _, part := range required {
		if !strings.Contains(HTTPMetricsQuery, part) {
			t.Fatalf("expected query to contain %q", part)
		}
	}
}

func TestFirewallMetricsQueryContainsExpectedFields(t *testing.T) {
	required := []string{
		"firewallEventsAdaptiveGroups",
		"clientRequestHTTPHost",
		"action",
		"count",
	}
	for _, part := range required {
		if !strings.Contains(FirewallMetricsQuery, part) {
			t.Fatalf("expected firewall query to contain %q", part)
		}
	}
}

func TestGatewayMetricsQueryContainsExpectedFields(t *testing.T) {
	required := []string{
		"gatewayL7RequestsAdaptiveGroups",
		"httpHost",
		"action",
		"count",
	}
	for _, part := range required {
		if !strings.Contains(GatewayL7MetricsQuery, part) {
			t.Fatalf("expected gateway query to contain %q", part)
		}
	}
}

func TestBuildGraphQLRequest(t *testing.T) {
	from := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Minute)

	req := BuildGraphQLRequest("zone_abc", []string{"a.com", "b.com"}, from, to)
	if req.Query == "" {
		t.Fatal("expected query to be present")
	}
	if req.Variables.ZoneTag != "zone_abc" {
		t.Fatalf("unexpected zone tag: %s", req.Variables.ZoneTag)
	}
	if req.Variables.From != "2026-02-20T10:00:00Z" {
		t.Fatalf("unexpected from value: %s", req.Variables.From)
	}
	if req.Variables.To != "2026-02-20T10:02:00Z" {
		t.Fatalf("unexpected to value: %s", req.Variables.To)
	}
}

func TestBuildGatewayGraphQLRequest(t *testing.T) {
	from := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Minute)

	req := BuildGatewayGraphQLRequest("account_abc", []string{"a.com", "b.com"}, from, to)
	if req.Query == "" {
		t.Fatal("expected query to be present")
	}
	if req.Variables.AccountTag != "account_abc" {
		t.Fatalf("unexpected account tag: %s", req.Variables.AccountTag)
	}
	if req.Variables.From != "2026-02-20T10:00:00Z" {
		t.Fatalf("unexpected from value: %s", req.Variables.From)
	}
	if req.Variables.To != "2026-02-20T10:02:00Z" {
		t.Fatalf("unexpected to value: %s", req.Variables.To)
	}
}
