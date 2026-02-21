package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasongodson/cloudflare-metrics-exporter/internal/cloudflare"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/config"
	"github.com/jasongodson/cloudflare-metrics-exporter/internal/metrics"
)

const (
	queryHTTP     = "http"
	queryFirewall = "firewall"
	queryGateway  = "gateway_l7"
)

var ErrPollInProgress = errors.New("poll already in progress")

type Client interface {
	FetchHTTPMetrics(ctx context.Context, zoneTag string, hosts []string, from, to time.Time) (map[string]cloudflare.HostMetrics, error)
	FetchFirewallMetrics(ctx context.Context, zoneTag string, hosts []string, from, to time.Time) (map[string]map[string]float64, error)
	FetchGatewayL7Metrics(ctx context.Context, accountTag string, hosts []string, from, to time.Time) (map[string]map[string]float64, error)
}

type Poller struct {
	cfg     config.Config
	client  Client
	metrics *metrics.Registry
	logger  *slog.Logger

	now         func() time.Time
	sleep       func(context.Context, time.Duration) bool
	rand        *rand.Rand
	maxAttempts int
	jitterMax   time.Duration
	inFlight    atomic.Bool
}

func New(cfg config.Config, client Client, registry *metrics.Registry, logger *slog.Logger) *Poller {
	if logger == nil {
		logger = slog.Default()
	}

	return &Poller{
		cfg:         cfg,
		client:      client,
		metrics:     registry,
		logger:      logger,
		now:         time.Now,
		sleep:       sleepWithContext,
		rand:        rand.New(rand.NewSource(time.Now().UnixNano())),
		maxAttempts: 4,
		jitterMax:   250 * time.Millisecond,
	}
}

func (p *Poller) Run(ctx context.Context) {
	if err := p.PollOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		p.logger.Error("cloudflare poll failed", "error", err)
	}

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.PollOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				p.logger.Error("cloudflare poll failed", "error", err)
			}
		}
	}
}

func (p *Poller) PollOnce(ctx context.Context) error {
	if !p.inFlight.CompareAndSwap(false, true) {
		p.logger.Warn("poll already in progress; skipping")
		return ErrPollInProgress
	}
	defer p.inFlight.Store(false)

	start := time.Now()
	pollTime := p.now().UTC()
	from := pollTime.Add(-p.cfg.Window)

	allQueriesSucceeded := true

	httpByZone := make(map[string]map[string]metrics.HostWindowMetrics, len(p.cfg.ZoneTags))
	firewallByZone := make(map[string]map[string]map[string]float64, len(p.cfg.ZoneTags))

	for _, zoneTag := range p.cfg.ZoneTags {
		httpResult, err := p.fetchHTTPWithRetry(ctx, zoneTag, from, pollTime)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			allQueriesSucceeded = false
			p.metrics.SetQueryUp(queryHTTP, zoneTag, false)
			p.metrics.IncQueryErrors(queryHTTP, zoneTag)
			p.logger.Warn("http metrics query failed", "zone", zoneTag, "error", err)
		} else {
			p.metrics.SetQueryUp(queryHTTP, zoneTag, true)
			converted := make(map[string]metrics.HostWindowMetrics, len(httpResult))
			for host, values := range httpResult {
				converted[host] = metrics.HostWindowMetrics{
					Requests:          values.Requests,
					EdgeResponseBytes: values.EdgeResponseBytes,
					CachedRequests:    values.CachedRequests,
					StatusByClass:     values.StatusRequests,
				}
			}
			httpByZone[zoneTag] = converted
		}

		firewallResult, err := p.fetchFirewallWithRetry(ctx, zoneTag, from, pollTime)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			allQueriesSucceeded = false
			p.metrics.SetQueryUp(queryFirewall, zoneTag, false)
			p.metrics.IncQueryErrors(queryFirewall, zoneTag)
			p.logger.Warn("firewall metrics query failed", "zone", zoneTag, "error", err)
		} else {
			p.metrics.SetQueryUp(queryFirewall, zoneTag, true)
			firewallByZone[zoneTag] = firewallResult
		}
	}

	p.metrics.UpdateZoneWindows(p.cfg.Hosts, httpByZone)
	p.metrics.UpdateFirewallWindows(p.cfg.Hosts, firewallByZone)

	if strings.TrimSpace(p.cfg.AccountTag) != "" {
		gatewayResult, err := p.fetchGatewayWithRetry(ctx, from, pollTime)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			allQueriesSucceeded = false
			p.metrics.SetQueryUp(queryGateway, p.cfg.AccountTag, false)
			p.metrics.IncQueryErrors(queryGateway, p.cfg.AccountTag)
			p.metrics.UpdateGatewayWindow(p.cfg.AccountTag, p.cfg.Hosts, nil)
			p.logger.Warn("gateway l7 metrics query failed", "account", p.cfg.AccountTag, "error", err)
		} else {
			p.metrics.SetQueryUp(queryGateway, p.cfg.AccountTag, true)
			p.metrics.UpdateGatewayWindow(p.cfg.AccountTag, p.cfg.Hosts, gatewayResult)
		}
	} else {
		p.metrics.UpdateGatewayWindow("", p.cfg.Hosts, nil)
	}

	if allQueriesSucceeded {
		p.metrics.SetLastSuccess(pollTime)
	} else {
		p.metrics.IncPollErrors()
	}

	p.metrics.SetExporterUp(true)
	p.metrics.SetPollDuration(time.Since(start))
	return nil
}

func (p *Poller) fetchHTTP(ctx context.Context, zoneTag string, from, to time.Time) (map[string]cloudflare.HostMetrics, error) {
	requestCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout)
	defer cancel()
	return p.client.FetchHTTPMetrics(requestCtx, zoneTag, p.cfg.Hosts, from, to)
}

func (p *Poller) fetchFirewall(ctx context.Context, zoneTag string, from, to time.Time) (map[string]map[string]float64, error) {
	requestCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout)
	defer cancel()
	return p.client.FetchFirewallMetrics(requestCtx, zoneTag, p.cfg.Hosts, from, to)
}

func (p *Poller) fetchGateway(ctx context.Context, from, to time.Time) (map[string]map[string]float64, error) {
	requestCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout)
	defer cancel()
	return p.client.FetchGatewayL7Metrics(requestCtx, p.cfg.AccountTag, p.cfg.Hosts, from, to)
}

func (p *Poller) fetchHTTPWithRetry(ctx context.Context, zoneTag string, from, to time.Time) (map[string]cloudflare.HostMetrics, error) {
	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		result, err := p.fetchHTTP(ctx, zoneTag, from, to)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		lastErr = err
		if attempt == p.maxAttempts {
			break
		}
		if !p.sleep(ctx, p.backoffDelay(attempt)) {
			return nil, context.Canceled
		}
	}
	return nil, fmt.Errorf("http query failed after %d attempts: %w", p.maxAttempts, lastErr)
}

func (p *Poller) fetchFirewallWithRetry(ctx context.Context, zoneTag string, from, to time.Time) (map[string]map[string]float64, error) {
	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		result, err := p.fetchFirewall(ctx, zoneTag, from, to)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		lastErr = err
		if attempt == p.maxAttempts {
			break
		}
		if !p.sleep(ctx, p.backoffDelay(attempt)) {
			return nil, context.Canceled
		}
	}
	return nil, fmt.Errorf("firewall query failed after %d attempts: %w", p.maxAttempts, lastErr)
}

func (p *Poller) fetchGatewayWithRetry(ctx context.Context, from, to time.Time) (map[string]map[string]float64, error) {
	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		result, err := p.fetchGateway(ctx, from, to)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		lastErr = err
		if attempt == p.maxAttempts {
			break
		}
		if !p.sleep(ctx, p.backoffDelay(attempt)) {
			return nil, context.Canceled
		}
	}
	return nil, fmt.Errorf("gateway query failed after %d attempts: %w", p.maxAttempts, lastErr)
}

func (p *Poller) backoffDelay(attempt int) time.Duration {
	base := time.Second * time.Duration(1<<(attempt-1))
	if p.jitterMax <= 0 {
		return base
	}
	jitter := time.Duration(p.rand.Int63n(int64(p.jitterMax)))
	return base + jitter
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
