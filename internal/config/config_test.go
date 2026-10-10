package config

import (
	"testing"
	"time"
)

func TestLoadFromEnvDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.PollInterval != DefaultPollInterval {
		t.Fatalf("expected default poll interval %s, got %s", DefaultPollInterval, cfg.PollInterval)
	}
	if cfg.Window != DefaultWindow {
		t.Fatalf("expected default window %s, got %s", DefaultWindow, cfg.Window)
	}
	if cfg.HTTPAddr != DefaultHTTPAddr {
		t.Fatalf("expected default http addr %s, got %s", DefaultHTTPAddr, cfg.HTTPAddr)
	}
	if cfg.RequestTimeout != DefaultRequestTimeout {
		t.Fatalf("expected default request timeout %s, got %s", DefaultRequestTimeout, cfg.RequestTimeout)
	}
	if cfg.LogLevel != DefaultLogLevel {
		t.Fatalf("expected default log level %s, got %s", DefaultLogLevel, cfg.LogLevel)
	}
	if !cfg.EnabledQueries[QueryHTTP] || !cfg.EnabledQueries[QueryFirewall] || !cfg.EnabledQueries[QueryGateway] {
		t.Fatalf("expected default enabled queries to include http, firewall, gateway_l7")
	}
	if cfg.QueryDelay != DefaultQueryDelay {
		t.Fatalf("expected default query delay %s, got %s", DefaultQueryDelay, cfg.QueryDelay)
	}
	if cfg.ZoneRefresh != DefaultZoneRefresh {
		t.Fatalf("expected default zone refresh %s, got %s", DefaultZoneRefresh, cfg.ZoneRefresh)
	}
}

func TestLoadFromEnvOnlyTokenRequired(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CF_ZONE_TAGS", "")
	t.Setenv("CF_HOSTS", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("expected zone tags and hosts to be optional, got %v", err)
	}
	if len(cfg.ZoneTags) != 0 || len(cfg.Hosts) != 0 {
		t.Fatalf("expected empty zone tags and hosts, got %v %v", cfg.ZoneTags, cfg.Hosts)
	}

	t.Setenv("CF_API_TOKEN", "")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error when CF_API_TOKEN is missing")
	}
}

func TestLoadFromEnvQueryDelayValidation(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("QUERY_DELAY", "0s")
	if _, err := LoadFromEnv(); err != nil {
		t.Fatalf("expected zero query delay to be allowed, got %v", err)
	}

	t.Setenv("QUERY_DELAY", "11m")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error for too large query delay")
	}
}

func TestLoadFromEnvZoneRefreshValidation(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ZONE_REFRESH_INTERVAL", "30s")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error for too small zone refresh interval")
	}
}

func TestLoadFromEnvHostParsing(t *testing.T) {
	t.Setenv("CF_API_TOKEN", "token")
	t.Setenv("CF_ZONE_TAGS", "zone-a")
	t.Setenv("CF_HOSTS", " example.com, Docs.Example.com ,example.com,,templates.example.com ")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(cfg.Hosts) != 3 {
		t.Fatalf("expected 3 unique hosts, got %d", len(cfg.Hosts))
	}
	if cfg.Hosts[1] != "docs.example.com" {
		t.Fatalf("expected hosts to be lowercased, got %q", cfg.Hosts[1])
	}
}

func TestLoadFromEnvZoneTagsParsing(t *testing.T) {
	t.Setenv("CF_API_TOKEN", "token")
	t.Setenv("CF_ZONE_TAGS", " zone-a,zone-b,zone-a , zone-c ")
	t.Setenv("CF_HOSTS", "example.com")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(cfg.ZoneTags) != 3 {
		t.Fatalf("expected 3 unique zone tags, got %d", len(cfg.ZoneTags))
	}
}

func TestLoadFromEnvPollIntervalValidation(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("POLL_INTERVAL", "10s")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for too small poll interval")
	}
}

func TestLoadFromEnvWindowValidation(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("POLL_INTERVAL", "90s")
	t.Setenv("WINDOW", "1m")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when window < poll interval")
	}

	setRequiredEnv(t)
	t.Setenv("WINDOW", (16 * time.Minute).String())
	_, err = LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when window is too large")
	}
}

func TestLoadFromEnvInvalidLogLevel(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LOG_LEVEL", "trace")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestLoadFromEnvPort(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PORT", "9090")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("expected http addr to be :9090, got %s", cfg.HTTPAddr)
	}
}

func TestLoadFromEnvInvalidPort(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PORT", ":")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestLoadFromEnvEnabledQueries(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ENABLED_QUERIES", "http,gateway")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !cfg.EnabledQueries[QueryHTTP] {
		t.Fatalf("expected http query to be enabled")
	}
	if cfg.EnabledQueries[QueryFirewall] {
		t.Fatalf("expected firewall query to be disabled")
	}
	if !cfg.EnabledQueries[QueryGateway] {
		t.Fatalf("expected gateway query to be enabled")
	}
}

func TestLoadFromEnvInvalidEnabledQueries(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ENABLED_QUERIES", "http,invalid")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for invalid ENABLED_QUERIES")
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CF_API_TOKEN", "token")
	t.Setenv("CF_ZONE_TAGS", "zone")
	t.Setenv("CF_HOSTS", "example.com,docs.example.com,templates.example.com")
	t.Setenv("POLL_INTERVAL", "")
	t.Setenv("WINDOW", "")
	t.Setenv("PORT", "")
	t.Setenv("REQUEST_TIMEOUT", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("ENABLED_QUERIES", "")
	t.Setenv("ZONE_REFRESH_INTERVAL", "")
	t.Setenv("QUERY_DELAY", "")
}
