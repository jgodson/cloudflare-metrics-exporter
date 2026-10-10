package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGraphQLEndpoint = "https://api.cloudflare.com/client/v4/graphql"
	defaultRESTEndpoint    = "https://api.cloudflare.com/client/v4"
	zonesPerPage           = 50
)

type Options struct {
	Endpoint     string
	RESTEndpoint string
	HTTPClient   *http.Client
	Logger       *slog.Logger
}

type Client struct {
	apiToken     string
	endpoint     string
	restEndpoint string
	httpClient   *http.Client
	logger       *slog.Logger
}

func NewClient(apiToken string, timeout time.Duration, opts Options) *Client {
	endpoint := opts.Endpoint
	if strings.TrimSpace(endpoint) == "" {
		endpoint = defaultGraphQLEndpoint
	}

	restEndpoint := strings.TrimRight(opts.RESTEndpoint, "/")
	if strings.TrimSpace(restEndpoint) == "" {
		restEndpoint = defaultRESTEndpoint
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Client{
		apiToken:     apiToken,
		endpoint:     endpoint,
		restEndpoint: restEndpoint,
		httpClient:   httpClient,
		logger:       logger,
	}
}

// ListZones returns all active zones visible to the API token. It requires the Zone Read permission.
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	var zones []Zone
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("status", "active")
		query.Set("per_page", strconv.Itoa(zonesPerPage))
		query.Set("page", strconv.Itoa(page))

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.restEndpoint+"/zones?"+query.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("build cloudflare zones request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiToken)

		var parsed zonesResponse
		if err := c.do(req, &parsed); err != nil {
			return nil, err
		}
		if !parsed.Success {
			return nil, combineGraphQLErrors(parsed.Errors)
		}

		for _, z := range parsed.Result {
			zones = append(zones, Zone{ID: z.ID, Name: strings.ToLower(z.Name)})
		}
		if page >= parsed.ResultInfo.TotalPages {
			return zones, nil
		}
	}
}

// FetchHTTPMetrics returns per-host metrics for every host in trackedHosts (zero-filled) plus
// an OtherHost entry that aggregates the rest of the zone's traffic.
func (c *Client) FetchHTTPMetrics(ctx context.Context, zoneTag string, trackedHosts []string, from, to time.Time) (map[string]HostMetrics, error) {
	payload := BuildGraphQLRequest(zoneTag, from, to)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal cloudflare graphql request: %w", err)
	}

	var parsed httpGraphQLResponse
	if err := c.doGraphQL(ctx, body, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Errors) > 0 {
		return nil, combineGraphQLErrors(parsed.Errors)
	}

	return aggregateHTTPMetrics(trackedHosts, parsed), nil
}

// FetchFirewallMetrics returns per-host action counts for trackedHosts plus an OtherHost entry
// for the rest of the zone.
func (c *Client) FetchFirewallMetrics(ctx context.Context, zoneTag string, trackedHosts []string, from, to time.Time) (map[string]map[string]float64, error) {
	payload := BuildFirewallGraphQLRequest(zoneTag, from, to)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal cloudflare firewall graphql request: %w", err)
	}

	var parsed firewallGraphQLResponse
	if err := c.doGraphQL(ctx, body, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Errors) > 0 {
		return nil, combineGraphQLErrors(parsed.Errors)
	}

	return aggregateZoneActionMetrics(trackedHosts, parsed), nil
}

func (c *Client) FetchGatewayL7Metrics(ctx context.Context, accountTag string, hosts []string, from, to time.Time) (map[string]map[string]float64, error) {
	payload := BuildGatewayGraphQLRequest(accountTag, hosts, from, to)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal cloudflare gateway graphql request: %w", err)
	}

	var parsed gatewayGraphQLResponse
	if err := c.doGraphQL(ctx, body, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Errors) > 0 {
		return nil, combineGraphQLErrors(parsed.Errors)
	}

	return aggregateGatewayActionMetrics(hosts, parsed), nil
}

func (c *Client) doGraphQL(ctx context.Context, body []byte, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build cloudflare graphql request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, dst)
}

func (c *Client) do(req *http.Request, dst any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute cloudflare request %s: %w", req.URL.Path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("cloudflare %s returned %s: %s", req.URL.Path, resp.Status, strings.TrimSpace(string(msg)))
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode cloudflare %s response: %w", req.URL.Path, err)
	}
	return nil
}

func aggregateHTTPMetrics(trackedHosts []string, response httpGraphQLResponse) map[string]HostMetrics {
	results := make(map[string]HostMetrics, len(trackedHosts)+1)
	for _, host := range trackedHosts {
		results[host] = HostMetrics{StatusRequests: map[string]float64{}}
	}
	results[OtherHost] = HostMetrics{StatusRequests: map[string]float64{}}

	if len(response.Data.Viewer.Zones) == 0 {
		return results
	}

	for _, group := range response.Data.Viewer.Zones[0].HTTPRequestsAdaptiveGroups {
		host := bucketHost(group.Dimensions.ClientRequestHTTPHost, results)
		hostMetrics := results[host]

		hostMetrics.Requests += group.Sum.Requests
		hostMetrics.EdgeResponseBytes += group.Sum.EdgeResponseBytes
		if isCachedResponse(group.Dimensions.CacheStatus) {
			hostMetrics.CachedRequests += group.Sum.Requests
		}
		if status := statusClass(group.Dimensions.EdgeResponseStatus); status != "" {
			hostMetrics.StatusRequests[status] += group.Sum.Requests
		}
		results[host] = hostMetrics
	}

	return results
}

func aggregateZoneActionMetrics(trackedHosts []string, response firewallGraphQLResponse) map[string]map[string]float64 {
	results := make(map[string]map[string]float64, len(trackedHosts)+1)
	for _, host := range trackedHosts {
		results[host] = map[string]float64{}
	}
	results[OtherHost] = map[string]float64{}

	if len(response.Data.Viewer.Zones) == 0 {
		return results
	}

	for _, group := range response.Data.Viewer.Zones[0].FirewallEventsAdaptiveGroups {
		action := strings.TrimSpace(group.Dimensions.Action)
		if action == "" {
			continue
		}
		host := bucketHost(group.Dimensions.ClientRequestHTTPHost, results)
		results[host][action] += group.Count
	}

	return results
}

// bucketHost maps a Cloudflare-reported host to its tracked host key, or OtherHost when untracked.
func bucketHost[V any](raw string, tracked map[string]V) string {
	host := strings.ToLower(strings.TrimSpace(raw))
	if _, ok := tracked[host]; ok {
		return host
	}
	return OtherHost
}

func aggregateGatewayActionMetrics(hosts []string, response gatewayGraphQLResponse) map[string]map[string]float64 {
	results := make(map[string]map[string]float64, len(hosts))
	for _, host := range hosts {
		results[host] = map[string]float64{}
	}

	allowedHosts := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		allowedHosts[host] = struct{}{}
	}

	if len(response.Data.Viewer.Accounts) == 0 {
		return results
	}

	for _, group := range response.Data.Viewer.Accounts[0].GatewayL7RequestsAdaptiveGroups {
		host := strings.TrimSpace(group.Dimensions.HTTPHost)
		action := strings.TrimSpace(group.Dimensions.Action)
		if host == "" || action == "" {
			continue
		}
		if _, ok := allowedHosts[host]; !ok {
			continue
		}
		if _, ok := results[host]; !ok {
			results[host] = map[string]float64{}
		}
		results[host][action] += group.Count
	}

	return results
}

func combineGraphQLErrors(errs []graphQLError) error {
	parts := make([]string, 0, len(errs))
	for _, item := range errs {
		if strings.TrimSpace(item.Message) == "" {
			continue
		}
		parts = append(parts, item.Message)
	}
	if len(parts) == 0 {
		return fmt.Errorf("cloudflare graphql returned unknown error")
	}
	return fmt.Errorf("cloudflare graphql errors: %s", strings.Join(parts, "; "))
}

func statusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return ""
	}
}

func isCachedResponse(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "hit", "stale", "revalidated", "updating":
		return true
	default:
		return false
	}
}

type httpGraphQLResponse struct {
	Data struct {
		Viewer struct {
			Zones []struct {
				HTTPRequestsAdaptiveGroups []struct {
					Dimensions struct {
						ClientRequestHTTPHost string `json:"clientRequestHTTPHost"`
						EdgeResponseStatus    int    `json:"edgeResponseStatus"`
						CacheStatus           string `json:"cacheStatus"`
					} `json:"dimensions"`
					Sum struct {
						Requests          float64 `json:"visits"`
						EdgeResponseBytes float64 `json:"edgeResponseBytes"`
					} `json:"sum"`
				} `json:"httpRequestsAdaptiveGroups"`
			} `json:"zones"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type firewallGraphQLResponse struct {
	Data struct {
		Viewer struct {
			Zones []struct {
				FirewallEventsAdaptiveGroups []struct {
					Dimensions struct {
						Action                string `json:"action"`
						ClientRequestHTTPHost string `json:"clientRequestHTTPHost"`
					} `json:"dimensions"`
					Count float64 `json:"count"`
				} `json:"firewallEventsAdaptiveGroups"`
			} `json:"zones"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type gatewayGraphQLResponse struct {
	Data struct {
		Viewer struct {
			Accounts []struct {
				GatewayL7RequestsAdaptiveGroups []struct {
					Dimensions struct {
						Action   string `json:"action"`
						HTTPHost string `json:"httpHost"`
					} `json:"dimensions"`
					Count float64 `json:"count"`
				} `json:"gatewayL7RequestsAdaptiveGroups"`
			} `json:"accounts"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type zonesResponse struct {
	Success bool `json:"success"`
	Result  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
	ResultInfo struct {
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
	Errors []graphQLError `json:"errors"`
}

type graphQLError struct {
	Message string `json:"message"`
}
