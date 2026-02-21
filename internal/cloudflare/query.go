package cloudflare

import "time"

const HTTPMetricsQuery = `
query ZoneHTTPMetrics($zoneTag: string!, $hosts: [string!], $from: Time!, $to: Time!) {
  viewer {
    zones(filter: { zoneTag: $zoneTag }) {
      httpRequestsAdaptiveGroups(
        limit: 10000
        filter: {
          datetime_geq: $from
          datetime_lt: $to
          clientRequestHTTPHost_in: $hosts
        }
      ) {
        dimensions {
          clientRequestHTTPHost
          edgeResponseStatus
          cacheStatus
        }
        sum {
          visits
          edgeResponseBytes
        }
      }
    }
  }
}
`

const FirewallMetricsQuery = `
query ZoneFirewallMetrics($zoneTag: string!, $hosts: [string!], $from: Time!, $to: Time!) {
  viewer {
    zones(filter: { zoneTag: $zoneTag }) {
      firewallEventsAdaptiveGroups(
        limit: 10000
        filter: {
          datetime_geq: $from
          datetime_lt: $to
          clientRequestHTTPHost_in: $hosts
        }
      ) {
        dimensions {
          action
          clientRequestHTTPHost
        }
        count
      }
    }
  }
}
`

const GatewayL7MetricsQuery = `
query AccountGatewayL7Metrics($accountTag: string!, $hosts: [string!], $from: Time!, $to: Time!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      gatewayL7RequestsAdaptiveGroups(
        limit: 10000
        filter: {
          datetime_geq: $from
          datetime_lt: $to
          httpHost_in: $hosts
        }
      ) {
        dimensions {
          action
          httpHost
        }
        count
      }
    }
  }
}
`

type GraphQLRequest struct {
	Query     string         `json:"query"`
	Variables QueryVariables `json:"variables"`
}

type AccountGraphQLRequest struct {
	Query     string                `json:"query"`
	Variables AccountQueryVariables `json:"variables"`
}

type QueryVariables struct {
	ZoneTag string   `json:"zoneTag"`
	Hosts   []string `json:"hosts"`
	From    string   `json:"from"`
	To      string   `json:"to"`
}

type AccountQueryVariables struct {
	AccountTag string   `json:"accountTag"`
	Hosts      []string `json:"hosts"`
	From       string   `json:"from"`
	To         string   `json:"to"`
}

func BuildQueryVariables(zoneTag string, hosts []string, from, to time.Time) QueryVariables {
	return QueryVariables{
		ZoneTag: zoneTag,
		Hosts:   hosts,
		From:    from.UTC().Format(time.RFC3339),
		To:      to.UTC().Format(time.RFC3339),
	}
}

func BuildAccountQueryVariables(accountTag string, hosts []string, from, to time.Time) AccountQueryVariables {
	return AccountQueryVariables{
		AccountTag: accountTag,
		Hosts:      hosts,
		From:       from.UTC().Format(time.RFC3339),
		To:         to.UTC().Format(time.RFC3339),
	}
}

func BuildGraphQLRequest(zoneTag string, hosts []string, from, to time.Time) GraphQLRequest {
	return GraphQLRequest{
		Query:     HTTPMetricsQuery,
		Variables: BuildQueryVariables(zoneTag, hosts, from, to),
	}
}

func BuildFirewallGraphQLRequest(zoneTag string, hosts []string, from, to time.Time) GraphQLRequest {
	return GraphQLRequest{
		Query:     FirewallMetricsQuery,
		Variables: BuildQueryVariables(zoneTag, hosts, from, to),
	}
}

func BuildGatewayGraphQLRequest(accountTag string, hosts []string, from, to time.Time) AccountGraphQLRequest {
	return AccountGraphQLRequest{
		Query:     GatewayL7MetricsQuery,
		Variables: BuildAccountQueryVariables(accountTag, hosts, from, to),
	}
}
