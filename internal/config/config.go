package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPollInterval   = 60 * time.Second
	MinPollInterval       = 30 * time.Second
	DefaultWindow         = 1 * time.Minute
	MaxWindow             = 15 * time.Minute
	DefaultHTTPAddr       = ":8080"
	DefaultRequestTimeout = 10 * time.Second
	DefaultLogLevel       = "info"

	QueryHTTP     = "http"
	QueryFirewall = "firewall"
	QueryGateway  = "gateway_l7"
)

type Config struct {
	APIToken       string
	ZoneTags       []string
	AccountTag     string
	Hosts          []string
	PollInterval   time.Duration
	Window         time.Duration
	HTTPAddr       string
	RequestTimeout time.Duration
	LogLevel       string
	EnabledQueries map[string]bool
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		PollInterval:   DefaultPollInterval,
		Window:         DefaultWindow,
		HTTPAddr:       DefaultHTTPAddr,
		RequestTimeout: DefaultRequestTimeout,
		LogLevel:       DefaultLogLevel,
		EnabledQueries: defaultEnabledQueries(),
	}

	var errs []string
	cfg.APIToken = strings.TrimSpace(os.Getenv("CF_API_TOKEN"))
	cfg.ZoneTags = parseCSVUnique(strings.TrimSpace(os.Getenv("CF_ZONE_TAGS")))
	cfg.AccountTag = strings.TrimSpace(os.Getenv("CF_ACCOUNT_TAG"))

	if cfg.APIToken == "" {
		errs = append(errs, "CF_API_TOKEN is required")
	}
	if len(cfg.ZoneTags) == 0 {
		errs = append(errs, "CF_ZONE_TAGS is required and must contain at least one zone tag")
	}

	hostsRaw := strings.TrimSpace(os.Getenv("CF_HOSTS"))
	cfg.Hosts = parseHosts(hostsRaw)
	if len(cfg.Hosts) == 0 {
		errs = append(errs, "CF_HOSTS is required and must contain at least one host")
	}

	if value := strings.TrimSpace(os.Getenv("POLL_INTERVAL")); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			errs = append(errs, fmt.Sprintf("POLL_INTERVAL is invalid: %v", err))
		} else {
			cfg.PollInterval = d
		}
	}

	if value := strings.TrimSpace(os.Getenv("WINDOW")); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			errs = append(errs, fmt.Sprintf("WINDOW is invalid: %v", err))
		} else {
			cfg.Window = d
		}
	}

	if value := strings.TrimSpace(os.Getenv("PORT")); value != "" {
		addr, err := portToAddr(value)
		if err != nil {
			errs = append(errs, fmt.Sprintf("PORT is invalid: %v", err))
		} else {
			cfg.HTTPAddr = addr
		}
	}

	if value := strings.TrimSpace(os.Getenv("REQUEST_TIMEOUT")); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			errs = append(errs, fmt.Sprintf("REQUEST_TIMEOUT is invalid: %v", err))
		} else {
			cfg.RequestTimeout = d
		}
	}

	if value := strings.TrimSpace(os.Getenv("LOG_LEVEL")); value != "" {
		cfg.LogLevel = strings.ToLower(value)
	}

	if value := strings.TrimSpace(os.Getenv("ENABLED_QUERIES")); value != "" {
		queries, err := parseEnabledQueries(value)
		if err != nil {
			errs = append(errs, fmt.Sprintf("ENABLED_QUERIES is invalid: %v", err))
		} else {
			cfg.EnabledQueries = queries
		}
	}

	if cfg.PollInterval < MinPollInterval {
		errs = append(errs, fmt.Sprintf("POLL_INTERVAL must be >= %s", MinPollInterval))
	}
	if cfg.Window < cfg.PollInterval {
		errs = append(errs, "WINDOW must be >= POLL_INTERVAL")
	}
	if cfg.Window > MaxWindow {
		errs = append(errs, fmt.Sprintf("WINDOW must be <= %s", MaxWindow))
	}
	if cfg.RequestTimeout <= 0 {
		errs = append(errs, "REQUEST_TIMEOUT must be > 0")
	}
	if cfg.HTTPAddr == "" {
		errs = append(errs, "PORT must not be empty")
	}
	if !isValidLogLevel(cfg.LogLevel) {
		errs = append(errs, "LOG_LEVEL must be one of: debug, info, warn, error")
	}

	if len(errs) > 0 {
		return Config{}, errors.New(strings.Join(errs, "; "))
	}

	return cfg, nil
}

func parseHosts(raw string) []string {
	return parseCSVUnique(raw)
}

func parseCSVUnique(raw string) []string {
	if raw == "" {
		return nil
	}

	hosts := strings.Split(raw, ",")
	result := make([]string, 0, len(hosts))
	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		h := strings.TrimSpace(host)
		if h == "" {
			continue
		}
		if _, exists := seen[h]; exists {
			continue
		}
		seen[h] = struct{}{}
		result = append(result, h)
	}
	return result
}

func isValidLogLevel(level string) bool {
	switch strings.ToLower(level) {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func portToAddr(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("must not be empty")
	}

	// Accept plain port numbers and bind all interfaces.
	if !strings.Contains(value, ":") {
		if err := validatePort(value); err != nil {
			return "", err
		}
		return ":" + value, nil
	}

	// Also allow explicit host:port style values.
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", err
	}
	if host == "" && port == "" {
		return "", errors.New("missing port")
	}
	if err := validatePort(port); err != nil {
		return "", err
	}
	return value, nil
}

func validatePort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("port must be numeric: %w", err)
	}
	if port <= 0 || port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

func defaultEnabledQueries() map[string]bool {
	return map[string]bool{
		QueryHTTP:     true,
		QueryFirewall: true,
		QueryGateway:  true,
	}
}

func parseEnabledQueries(raw string) (map[string]bool, error) {
	result := make(map[string]bool)
	var invalid []string

	for _, item := range strings.Split(raw, ",") {
		query := strings.ToLower(strings.TrimSpace(item))
		if query == "" {
			continue
		}
		if query == "gateway" {
			query = QueryGateway
		}

		switch query {
		case QueryHTTP, QueryFirewall, QueryGateway:
			result[query] = true
		default:
			invalid = append(invalid, query)
		}
	}

	if len(invalid) > 0 {
		sort.Strings(invalid)
		return nil, fmt.Errorf("unknown values: %s", strings.Join(invalid, ", "))
	}
	if len(result) == 0 {
		return nil, errors.New("must include at least one query (http, firewall, gateway_l7)")
	}

	return result, nil
}
