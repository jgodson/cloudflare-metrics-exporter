package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const defaultGraphQLEndpoint = "https://api.cloudflare.com/client/v4/graphql"

type Options struct {
	Endpoint   string
	HTTPClient *http.Client
	Logger     *slog.Logger
}

type Client struct {
	apiToken   string
	endpoint   string
	httpClient *http.Client
	logger     *slog.Logger
}

func NewClient(apiToken string, timeout time.Duration, opts Options) *Client {
	endpoint := opts.Endpoint
	if strings.TrimSpace(endpoint) == "" {
		endpoint = defaultGraphQLEndpoint
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
		apiToken:   apiToken,
		endpoint:   endpoint,
		httpClient: httpClient,
		logger:     logger,
	}
}

func (c *Client) FetchHTTPMetrics(ctx context.Context, zoneTag string, hosts []string, from, to time.Time) (map[string]HostMetrics, error) {
	payload := BuildGraphQLRequest(zoneTag, hosts, from, to)
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

	return aggregateHTTPMetrics(hosts, parsed), nil
}

func (c *Client) FetchFirewallMetrics(ctx context.Context, zoneTag string, hosts []string, from, to time.Time) (map[string]map[string]float64, error) {
	payload := BuildFirewallGraphQLRequest(zoneTag, hosts, from, to)
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

	return aggregateZoneActionMetrics(hosts, parsed), nil
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute cloudflare graphql request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("cloudflare graphql returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode cloudflare graphql response: %w", err)
	}
	return nil
}

func aggregateHTTPMetrics(hosts []string, response httpGraphQLResponse) map[string]HostMetrics {
	results := make(map[string]HostMetrics, len(hosts))
	for _, host := range hosts {
		results[host] = HostMetrics{StatusRequests: map[string]float64{}}
	}

	allowedHosts := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		allowedHosts[host] = struct{}{}
	}

	if len(response.Data.Viewer.Zones) == 0 {
		return results
	}

	for _, group := range response.Data.Viewer.Zones[0].HTTPRequestsAdaptiveGroups {
		host := strings.TrimSpace(group.Dimensions.ClientRequestHTTPHost)
		if host == "" {
			continue
		}
		if _, ok := allowedHosts[host]; !ok {
			continue
		}

		hostMetrics := results[host]
		if hostMetrics.StatusRequests == nil {
			hostMetrics.StatusRequests = map[string]float64{}
		}

		hostMetrics.Requests += group.Sum.Requests
		hostMetrics.EdgeResponseBytes += group.Sum.EdgeResponseBytes
		hostMetrics.CachedRequests += group.Sum.CachedRequests
		if status := statusClass(group.Dimensions.EdgeResponseStatus); status != "" {
			hostMetrics.StatusRequests[status] += group.Sum.Requests
		}
		results[host] = hostMetrics
	}

	return results
}

func aggregateZoneActionMetrics(hosts []string, response firewallGraphQLResponse) map[string]map[string]float64 {
	results := make(map[string]map[string]float64, len(hosts))
	for _, host := range hosts {
		results[host] = map[string]float64{}
	}

	allowedHosts := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		allowedHosts[host] = struct{}{}
	}

	if len(response.Data.Viewer.Zones) == 0 {
		return results
	}

	for _, group := range response.Data.Viewer.Zones[0].FirewallEventsAdaptiveGroups {
		host := strings.TrimSpace(group.Dimensions.ClientRequestHTTPHost)
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

type httpGraphQLResponse struct {
	Data struct {
		Viewer struct {
			Zones []struct {
				HTTPRequestsAdaptiveGroups []struct {
					Dimensions struct {
						ClientRequestHTTPHost string `json:"clientRequestHTTPHost"`
						EdgeResponseStatus    int    `json:"edgeResponseStatus"`
					} `json:"dimensions"`
					Sum struct {
						Requests          float64 `json:"visits"`
						EdgeResponseBytes float64 `json:"edgeResponseBytes"`
						CachedRequests    float64 `json:"cachedRequests"`
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

type graphQLError struct {
	Message string `json:"message"`
}
