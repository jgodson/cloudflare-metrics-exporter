package metrics

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var statusClasses = []string{"2xx", "3xx", "4xx", "5xx"}

type HostWindowMetrics struct {
	Requests          float64
	EdgeResponseBytes float64
	CachedRequests    float64
	StatusByClass     map[string]float64
}

// ZoneHTTPWindow holds one zone's HTTP results keyed by host label.
type ZoneHTTPWindow struct {
	Zone     string
	ZoneName string
	Hosts    map[string]HostWindowMetrics
}

// ZoneFirewallWindow holds one zone's firewall action counts keyed by host label.
type ZoneFirewallWindow struct {
	Zone     string
	ZoneName string
	Hosts    map[string]map[string]float64
}

type Registry struct {
	registry *prometheus.Registry

	HTTPRequestsWindow          *prometheus.GaugeVec
	HTTPEdgeResponseBytesWindow *prometheus.GaugeVec
	HTTPCachedRequestsWindow    *prometheus.GaugeVec
	HTTPStatusRequestsWindow    *prometheus.GaugeVec
	FirewallEventsWindow        *prometheus.GaugeVec
	GatewayL7RequestsWindow     *prometheus.GaugeVec
	QueryUp                     *prometheus.GaugeVec
	QueryErrorsTotal            *prometheus.CounterVec

	ExporterUp          prometheus.Gauge
	ExporterLastSuccess prometheus.Gauge
	ExporterPollSeconds prometheus.Gauge
	ExporterPollErrors  prometheus.Counter
}

func NewRegistry() *Registry {
	r := &Registry{registry: prometheus.NewRegistry()}

	r.HTTPRequestsWindow = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "cf_http_requests_window", Help: "HTTP requests in the current lookback window."},
		[]string{"zone", "zone_name", "host"},
	)
	r.HTTPEdgeResponseBytesWindow = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "cf_http_edge_response_bytes_window", Help: "Edge response bytes in the current lookback window."},
		[]string{"zone", "zone_name", "host"},
	)
	r.HTTPCachedRequestsWindow = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "cf_http_cached_requests_window", Help: "Cached requests in the current lookback window."},
		[]string{"zone", "zone_name", "host"},
	)
	r.HTTPStatusRequestsWindow = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "cf_http_status_requests_window", Help: "HTTP requests grouped by status class in the current window."},
		[]string{"zone", "zone_name", "host", "status_class"},
	)
	r.FirewallEventsWindow = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "cf_firewall_events_window", Help: "Firewall events in the current lookback window grouped by host and action."},
		[]string{"zone", "zone_name", "host", "action"},
	)
	r.GatewayL7RequestsWindow = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "cf_gateway_l7_requests_window", Help: "Gateway L7 requests in the current lookback window grouped by host and action."},
		[]string{"account", "host", "action"},
	)
	r.QueryUp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "cf_exporter_query_up", Help: "Whether the most recent run of a specific query succeeded (1) or failed (0)."},
		[]string{"query", "target"},
	)
	r.QueryErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "cf_exporter_query_errors_total", Help: "Total number of failed query runs by query type."},
		[]string{"query", "target"},
	)

	r.ExporterUp = prometheus.NewGauge(prometheus.GaugeOpts{Name: "cf_exporter_up", Help: "Exporter process health (1 when process is running and serving metrics)."})
	r.ExporterLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{Name: "cf_exporter_last_success_unixtime", Help: "Unix timestamp for the most recent successful poll."})
	r.ExporterPollSeconds = prometheus.NewGauge(prometheus.GaugeOpts{Name: "cf_exporter_poll_duration_seconds", Help: "Duration in seconds of the most recent polling cycle."})
	r.ExporterPollErrors = prometheus.NewCounter(prometheus.CounterOpts{Name: "cf_exporter_poll_errors_total", Help: "Total number of polling cycles where at least one enabled query failed."})

	r.registry.MustRegister(
		r.HTTPRequestsWindow,
		r.HTTPEdgeResponseBytesWindow,
		r.HTTPCachedRequestsWindow,
		r.HTTPStatusRequestsWindow,
		r.FirewallEventsWindow,
		r.GatewayL7RequestsWindow,
		r.QueryUp,
		r.QueryErrorsTotal,
		r.ExporterUp,
		r.ExporterLastSuccess,
		r.ExporterPollSeconds,
		r.ExporterPollErrors,
	)

	r.ExporterUp.Set(1)

	return r
}

func (r *Registry) Gatherer() prometheus.Gatherer {
	return r.registry
}

func (r *Registry) UpdateZoneWindows(zones []ZoneHTTPWindow) {
	r.HTTPRequestsWindow.Reset()
	r.HTTPEdgeResponseBytesWindow.Reset()
	r.HTTPCachedRequestsWindow.Reset()
	r.HTTPStatusRequestsWindow.Reset()

	for _, zone := range zones {
		for host, hostData := range zone.Hosts {
			r.HTTPRequestsWindow.WithLabelValues(zone.Zone, zone.ZoneName, host).Set(hostData.Requests)
			r.HTTPEdgeResponseBytesWindow.WithLabelValues(zone.Zone, zone.ZoneName, host).Set(hostData.EdgeResponseBytes)
			r.HTTPCachedRequestsWindow.WithLabelValues(zone.Zone, zone.ZoneName, host).Set(hostData.CachedRequests)
			for _, class := range statusClasses {
				r.HTTPStatusRequestsWindow.WithLabelValues(zone.Zone, zone.ZoneName, host, class).Set(hostData.StatusByClass[class])
			}
		}
	}
}

func (r *Registry) UpdateFirewallWindows(zones []ZoneFirewallWindow) {
	r.FirewallEventsWindow.Reset()

	for _, zone := range zones {
		for host, actions := range zone.Hosts {
			for action, value := range actions {
				a := strings.TrimSpace(action)
				if a == "" {
					continue
				}
				r.FirewallEventsWindow.WithLabelValues(zone.Zone, zone.ZoneName, host, a).Set(value)
			}
		}
	}
}

func (r *Registry) UpdateGatewayWindow(account string, hosts []string, data map[string]map[string]float64) {
	r.GatewayL7RequestsWindow.Reset()
	if strings.TrimSpace(account) == "" {
		return
	}

	for _, host := range hosts {
		actions, ok := data[host]
		if !ok {
			continue
		}
		for action, value := range actions {
			a := strings.TrimSpace(action)
			if a == "" {
				continue
			}
			r.GatewayL7RequestsWindow.WithLabelValues(account, host, a).Set(value)
		}
	}
}

func (r *Registry) SetExporterUp(up bool) {
	if up {
		r.ExporterUp.Set(1)
		return
	}
	r.ExporterUp.Set(0)
}

func (r *Registry) SetLastSuccess(t time.Time) {
	r.ExporterLastSuccess.Set(float64(t.Unix()))
}

func (r *Registry) SetPollDuration(d time.Duration) {
	r.ExporterPollSeconds.Set(d.Seconds())
}

func (r *Registry) IncPollErrors() {
	r.ExporterPollErrors.Inc()
}

func (r *Registry) SetQueryUp(query, target string, up bool) {
	if up {
		r.QueryUp.WithLabelValues(query, target).Set(1)
		return
	}
	r.QueryUp.WithLabelValues(query, target).Set(0)
}

// DeleteQuerySeries removes query health series for a target that is no longer polled.
func (r *Registry) DeleteQuerySeries(target string) {
	r.QueryUp.DeletePartialMatch(prometheus.Labels{"target": target})
	r.QueryErrorsTotal.DeletePartialMatch(prometheus.Labels{"target": target})
}

func (r *Registry) IncQueryErrors(query, target string) {
	r.QueryErrorsTotal.WithLabelValues(query, target).Inc()
}
