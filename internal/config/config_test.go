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
}

func TestLoadFromEnvHostParsing(t *testing.T) {
	t.Setenv("CF_API_TOKEN", "token")
	t.Setenv("CF_ZONE_TAGS", "zone-a")
	t.Setenv("CF_HOSTS", " example.com, docs.example.com ,example.com,,templates.example.com ")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(cfg.Hosts) != 3 {
		t.Fatalf("expected 3 unique hosts, got %d", len(cfg.Hosts))
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
}
