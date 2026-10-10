package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
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
	queryZones    = "zones"

	zonesTarget = "discovery"
)

var ErrPollInProgress = errors.New("poll already in progress")

type Client interface {
	ListZones(ctx context.Context) ([]cloudflare.Zone, error)
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

	zones            []zoneTarget
	zonesRefreshedAt time.Time
}

// zoneTarget is a zone to poll and the hosts reported individually for it.
type zoneTarget struct {
	Tag   string
	Name  string
	Hosts []string
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
	// Cloudflare analytics lag behind real time, so query a window that ends QueryDelay ago.
	to := pollTime.Add(-p.cfg.QueryDelay)
	from := to.Add(-p.cfg.Window)

	allQueriesSucceeded := true
	httpEnabled := p.queryEnabled(queryHTTP)
	firewallEnabled := p.queryEnabled(queryFirewall)
	gatewayEnabled := p.queryEnabled(queryGateway)

	if err := p.refreshZones(ctx, pollTime); err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		allQueriesSucceeded = false
	}

	var httpWindows []metrics.ZoneHTTPWindow
	var firewallWindows []metrics.ZoneFirewallWindow

	for _, zone := range p.zones {
		if httpEnabled {
			httpResult, err := p.fetchHTTPWithRetry(ctx, zone, from, to)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return context.Canceled
				}
				allQueriesSucceeded = false
				p.metrics.SetQueryUp(queryHTTP, zone.Tag, false)
				p.metrics.IncQueryErrors(queryHTTP, zone.Tag)
				p.logger.Warn("http metrics query failed", "zone", zone.Tag, "zone_name", zone.Name, "error", err)
			} else {
				p.metrics.SetQueryUp(queryHTTP, zone.Tag, true)
				converted := make(map[string]metrics.HostWindowMetrics, len(httpResult))
				for host, values := range httpResult {
					converted[host] = metrics.HostWindowMetrics{
						Requests:          values.Requests,
						Visits:            values.Visits,
						EdgeResponseBytes: values.EdgeResponseBytes,
						CachedRequests:    values.CachedRequests,
						StatusByClass:     values.StatusRequests,
					}
				}
				httpWindows = append(httpWindows, metrics.ZoneHTTPWindow{Zone: zone.Tag, ZoneName: zone.Name, Hosts: converted})
			}
		}

		if firewallEnabled {
			firewallResult, err := p.fetchFirewallWithRetry(ctx, zone, from, to)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return context.Canceled
				}
				allQueriesSucceeded = false
				p.metrics.SetQueryUp(queryFirewall, zone.Tag, false)
				p.metrics.IncQueryErrors(queryFirewall, zone.Tag)
				p.logger.Warn("firewall metrics query failed", "zone", zone.Tag, "zone_name", zone.Name, "error", err)
			} else {
				p.metrics.SetQueryUp(queryFirewall, zone.Tag, true)
				firewallWindows = append(firewallWindows, metrics.ZoneFirewallWindow{Zone: zone.Tag, ZoneName: zone.Name, Hosts: firewallResult})
			}
		}
	}

	p.metrics.UpdateZoneWindows(httpWindows)
	p.metrics.UpdateFirewallWindows(firewallWindows)

	if gatewayEnabled && strings.TrimSpace(p.cfg.AccountTag) != "" {
		gatewayResult, err := p.fetchGatewayWithRetry(ctx, from, to)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			allQueriesSucceeded = false
			p.metrics.SetQueryUp(queryGateway, p.cfg.AccountTag, false)
			p.metrics.IncQueryErrors(queryGateway, p.cfg.AccountTag)
			p.metrics.UpdateGatewayWindow(p.cfg.AccountTag, p.gatewayHosts(), nil)
			p.logger.Warn("gateway l7 metrics query failed", "account", p.cfg.AccountTag, "error", err)
		} else {
			p.metrics.SetQueryUp(queryGateway, p.cfg.AccountTag, true)
			p.metrics.UpdateGatewayWindow(p.cfg.AccountTag, p.gatewayHosts(), gatewayResult)
		}
	} else {
		p.metrics.UpdateGatewayWindow("", nil, nil)
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

// refreshZones re-discovers zones when the refresh interval has elapsed. On failure it keeps the
// previously discovered zones, or falls back to CF_ZONE_TAGS (without host-to-zone mapping) if
// discovery has never succeeded.
func (p *Poller) refreshZones(ctx context.Context, now time.Time) error {
	if !p.zonesRefreshedAt.IsZero() && now.Sub(p.zonesRefreshedAt) < p.cfg.ZoneRefresh {
		return nil
	}

	discovered, err := p.listZonesWithRetry(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		p.metrics.SetQueryUp(queryZones, zonesTarget, false)
		p.metrics.IncQueryErrors(queryZones, zonesTarget)
		if p.zones == nil && len(p.cfg.ZoneTags) > 0 {
			p.zones = fallbackZoneTargets(p.cfg.ZoneTags, p.cfg.Hosts)
			p.logger.Warn("zone discovery failed; polling CF_ZONE_TAGS without host-to-zone mapping (token needs Zone Read)", "error", err)
		} else {
			p.logger.Warn("zone discovery failed; keeping previous zones", "zones", len(p.zones), "error", err)
		}
		return err
	}

	p.metrics.SetQueryUp(queryZones, zonesTarget, true)
	p.zonesRefreshedAt = now

	targets, unmatched, missingTags := buildZoneTargets(discovered, p.cfg.ZoneTags, p.cfg.Hosts)
	if len(unmatched) > 0 {
		p.logger.Warn("hosts do not belong to any discovered zone and will not be reported", "hosts", unmatched)
	}
	if len(missingTags) > 0 {
		p.logger.Warn("CF_ZONE_TAGS entries not visible to the API token", "zone_tags", missingTags)
	}

	current := make(map[string]struct{}, len(targets))
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		current[t.Tag] = struct{}{}
		names = append(names, t.Name)
	}
	for _, old := range p.zones {
		if _, ok := current[old.Tag]; !ok {
			p.metrics.DeleteQuerySeries(old.Tag)
		}
	}
	if !sameZones(p.zones, targets) {
		p.logger.Info("zones discovered", "count", len(targets), "zones", names)
	}
	p.zones = targets
	return nil
}

// buildZoneTargets filters zones by filterTags (when set) and assigns each host to the zone with
// the longest matching domain suffix. Each zone's apex is always tracked.
func buildZoneTargets(zones []cloudflare.Zone, filterTags, hosts []string) (targets []zoneTarget, unmatchedHosts, missingTags []string) {
	if len(filterTags) > 0 {
		byID := make(map[string]cloudflare.Zone, len(zones))
		for _, z := range zones {
			byID[z.ID] = z
		}
		filtered := make([]cloudflare.Zone, 0, len(filterTags))
		for _, tag := range filterTags {
			z, ok := byID[tag]
			if !ok {
				missingTags = append(missingTags, tag)
				continue
			}
			filtered = append(filtered, z)
		}
		zones = filtered
	}

	sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })
	targets = make([]zoneTarget, len(zones))
	for i, z := range zones {
		targets[i] = zoneTarget{Tag: z.ID, Name: z.Name, Hosts: []string{z.Name}}
	}

	for _, host := range hosts {
		best := -1
		for i, t := range targets {
			if host != t.Name && !strings.HasSuffix(host, "."+t.Name) {
				continue
			}
			if best == -1 || len(t.Name) > len(targets[best].Name) {
				best = i
			}
		}
		if best == -1 {
			unmatchedHosts = append(unmatchedHosts, host)
			continue
		}
		if host != targets[best].Name {
			targets[best].Hosts = append(targets[best].Hosts, host)
		}
	}

	return targets, unmatchedHosts, missingTags
}

func fallbackZoneTargets(zoneTags, hosts []string) []zoneTarget {
	targets := make([]zoneTarget, len(zoneTags))
	for i, tag := range zoneTags {
		targets[i] = zoneTarget{Tag: tag, Hosts: hosts}
	}
	return targets
}

func sameZones(a, b []zoneTarget) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Tag != b[i].Tag || a[i].Name != b[i].Name || strings.Join(a[i].Hosts, ",") != strings.Join(b[i].Hosts, ",") {
			return false
		}
	}
	return true
}

// gatewayHosts returns configured hosts plus every tracked zone host.
func (p *Poller) gatewayHosts() []string {
	seen := make(map[string]struct{})
	var hosts []string
	add := func(h string) {
		if h == "" {
			return
		}
		if _, ok := seen[h]; ok {
			return
		}
		seen[h] = struct{}{}
		hosts = append(hosts, h)
	}
	for _, h := range p.cfg.Hosts {
		add(h)
	}
	for _, z := range p.zones {
		for _, h := range z.Hosts {
			add(h)
		}
	}
	return hosts
}

func (p *Poller) listZonesWithRetry(ctx context.Context) ([]cloudflare.Zone, error) {
	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		requestCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout)
		zones, err := p.client.ListZones(requestCtx)
		cancel()
		if err == nil {
			return zones, nil
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
	return nil, fmt.Errorf("zone discovery failed after %d attempts: %w", p.maxAttempts, lastErr)
}

func (p *Poller) fetchHTTP(ctx context.Context, zone zoneTarget, from, to time.Time) (map[string]cloudflare.HostMetrics, error) {
	requestCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout)
	defer cancel()
	return p.client.FetchHTTPMetrics(requestCtx, zone.Tag, zone.Hosts, from, to)
}

func (p *Poller) fetchFirewall(ctx context.Context, zone zoneTarget, from, to time.Time) (map[string]map[string]float64, error) {
	requestCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout)
	defer cancel()
	return p.client.FetchFirewallMetrics(requestCtx, zone.Tag, zone.Hosts, from, to)
}

func (p *Poller) fetchGateway(ctx context.Context, from, to time.Time) (map[string]map[string]float64, error) {
	requestCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout)
	defer cancel()
	return p.client.FetchGatewayL7Metrics(requestCtx, p.cfg.AccountTag, p.gatewayHosts(), from, to)
}

func (p *Poller) fetchHTTPWithRetry(ctx context.Context, zone zoneTarget, from, to time.Time) (map[string]cloudflare.HostMetrics, error) {
	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		result, err := p.fetchHTTP(ctx, zone, from, to)
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

func (p *Poller) fetchFirewallWithRetry(ctx context.Context, zone zoneTarget, from, to time.Time) (map[string]map[string]float64, error) {
	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		result, err := p.fetchFirewall(ctx, zone, from, to)
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

func (p *Poller) queryEnabled(query string) bool {
	if len(p.cfg.EnabledQueries) == 0 {
		return true
	}
	return p.cfg.EnabledQueries[query]
}
