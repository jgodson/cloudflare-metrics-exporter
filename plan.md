# Cloudflare Metrics Exporter Plan

Last updated: 2026-02-20

## Goal
Build a standalone service that polls Cloudflare GraphQL analytics and exposes Prometheus metrics for site traffic so Prometheus can scrape and alert on it.

Primary use case:
- Track request volume and bytes for:
1. `example.com` (marketing)
2. `docs.example.com` (docs)
3. `templates.example.com` (templates)

## Non-goals (v1)
- No write-back to Cloudflare.
- No long-term storage in exporter (Prometheus handles retention).
- No multi-tenant auth model.
- No UI beyond `/metrics` and optional `/healthz`.

## Architecture (v1)
1. Poll loop runs every 60s.
2. Poll loop queries Cloudflare GraphQL for a recent time window (default 1 minute).
3. Response is normalized by `host`.
4. Prometheus Gauges are updated with latest window values.
5. Prometheus scrapes exporter on `/metrics`.

Why gauges for v1:
- Cloudflare GraphQL returns bucketed aggregates, not an append-only stream.
- Gauges avoid double-counting and counter reset edge cases.

## Proposed repo structure
```text
cloudflare-metrics-exporter/
  cmd/exporter/main.go
  internal/config/config.go
  internal/cloudflare/client.go
  internal/cloudflare/query.go
  internal/cloudflare/types.go
  internal/poller/poller.go
  internal/metrics/registry.go
  internal/httpserver/server.go
  internal/logging/logging.go
  deploy/k8s/deployment.yaml
  deploy/k8s/service.yaml
  deploy/k8s/servicemonitor.yaml
  deploy/k8s/secret.example.yaml
  Dockerfile
  Makefile
  README.md
```

## Metrics to expose
Prefix: `cf_`

Core gauges:
1. `cf_http_requests_window{zone,host}`  
2. `cf_http_edge_response_bytes_window{zone,host}`  
3. `cf_http_cached_requests_window{zone,host}` (if field available)
4. `cf_http_status_requests_window{zone,host,status_class}` (2xx/3xx/4xx/5xx if available)

Exporter health:
1. `cf_exporter_up` (1/0)
2. `cf_exporter_last_success_unixtime`
3. `cf_exporter_poll_duration_seconds`
4. `cf_exporter_poll_errors_total`

## Cloudflare GraphQL query approach
Use one query per zone per poll with host filter:
- Filter by time window `[now-window, now)`
- Filter host in configured host list
- Group by `clientRequestHTTPHost`

Implementation detail:
- Keep query in `internal/cloudflare/query.go`.
- Keep GraphQL variables typed and explicit.
- Include unit test that validates query and variables map.

## Configuration (env vars)
Required:
1. `CF_API_TOKEN` - API token with analytics read permission.
2. `CF_ZONE_TAGS` - comma-separated Zone IDs to query.
3. `CF_HOSTS` - comma-separated hostnames to include.

Optional:
1. `CF_ACCOUNT_TAG` - if needed for account-scoped datasets.
2. `POLL_INTERVAL` - default `60s`.
3. `WINDOW` - default `1m`.
4. `PORT` - default `8080` (binds on `:PORT`).
5. `REQUEST_TIMEOUT` - default `10s`.
6. `LOG_LEVEL` - default `info`.

Validation rules:
- Interval must be >= 30s.
- Window must be >= interval and <= 15m.
- Hosts list must not be empty.

## Security
1. Store `CF_API_TOKEN` in Kubernetes Secret.
2. Never log token or full auth headers.
3. Use least-privilege token scope.
4. Run container as non-root, read-only rootfs.
5. Network egress only to Cloudflare API if network policies are used.

## Kubernetes deployment plan
Resources:
1. Deployment with 1 replica (start simple).
2. Service exposing port `8080`.
3. `ServiceMonitor` for Prometheus Operator environments.

Pod settings:
1. Liveness: `GET /healthz`
2. Readiness: `GET /readyz`
3. Scrape: `GET /metrics`
4. Requests/limits: small footprint (e.g. `50m/64Mi`, `200m/256Mi`).

## Reliability and rate limits
1. Use exponential backoff with jitter for failed polls.
2. Hard timeout per GraphQL request.
3. Keep one in-flight poll at a time.
4. Respect Cloudflare GraphQL limits (polling once per minute is safe).
5. On failures, keep last good gauge values and flip `cf_exporter_up=0`.

## Testing plan
Unit tests:
1. Config parsing and validation.
2. GraphQL query/variable generation.
3. Response parsing and host aggregation.
4. Metrics update behavior.

Integration tests:
1. Mock GraphQL HTTP server with success and error responses.
2. Assert `/metrics` output includes expected labels and values.
3. Assert backoff and failure handling metrics.

Manual test:
1. Run locally with a real token.
2. Curl `/metrics`.
3. Confirm values for all 3 hosts.

## Rollout plan
1. Deploy to non-prod namespace.
2. Confirm scrape target is `UP`.
3. Validate metrics in Prometheus expression browser.
4. Create Grafana panels by host.
5. Add alert for exporter failures:
   - `cf_exporter_up == 0` for 5m

## Suggested milestones
1. Milestone 1: Scaffold app, config, and `/metrics` endpoint.
2. Milestone 2: Cloudflare client + polling + core gauges.
3. Milestone 3: K8s manifests and Prometheus scraping.
4. Milestone 4: Dashboards, alerts, docs, and hardening.

## Definition of done
1. Prometheus can scrape exporter successfully.
2. Metrics exist for all three hosts.
3. Exporter health metrics reflect failures.
4. README includes setup, env vars, and deployment steps.
5. Basic tests pass in CI.

## Open decisions for implementer
1. Multi-zone behavior for host overlap and account-scoped datasets.
2. Exact dataset/fields available on current Cloudflare plan.
3. Whether to add optional status-class breakdown in v1 or v2.
4. Whether to ship a starter Grafana dashboard JSON in repo.
