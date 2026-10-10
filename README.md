# Cloudflare Metrics Exporter

Prometheus exporter that polls Cloudflare GraphQL analytics every minute and publishes per-host windowed traffic metrics.

## Features

- Polls Cloudflare once per minute (default) with one in-flight poll at a time.
- Discovers zones automatically from the API token, so new domains are picked up without config changes.
- Publishes `cf_http_*` per-host metrics and exporter health metrics. Each zone's apex plus any hosts in `CF_HOSTS` get their own series; all other traffic in the zone is reported as `host="other"`, so per-zone totals stay complete.
- Exposes `/metrics`, `/healthz`, and `/readyz` on `:8080` by default.
- Includes Docker and Kubernetes deployment manifests.
- Standalone service with no database dependency.

## Metrics

- `cf_http_requests_window{zone,zone_name,host}`
- `cf_http_edge_response_bytes_window{zone,zone_name,host}`
- `cf_http_cached_requests_window{zone,zone_name,host}`
- `cf_http_status_requests_window{zone,zone_name,host,status_class}`
- `cf_firewall_events_window{zone,zone_name,host,action}`
- `cf_gateway_l7_requests_window{account,host,action}` (when `CF_ACCOUNT_TAG` is set)
- `cf_exporter_query_up{query,target}` (`query` in `zones`, `http`, `firewall`, `gateway_l7`; `target` is `discovery`, zone tag, or account tag)
- `cf_exporter_query_errors_total{query,target}`
- `cf_exporter_up` (exporter process health; not query health)
- `cf_exporter_last_success_unixtime`
- `cf_exporter_poll_duration_seconds`
- `cf_exporter_poll_errors_total`

## Configuration

| Variable | Required | Default | Notes |
| --- | --- | --- | --- |
| `CF_API_TOKEN` | Yes | - | Cloudflare token with analytics read permissions. Store in a secret. |
| `CF_ZONE_TAGS` | No | all zones visible to the token | Comma-separated zone IDs to restrict polling to. Also used as a fallback (without host-to-zone mapping) if zone discovery fails before it has ever succeeded. |
| `CF_HOSTS` | No | - | Comma-separated hosts to report individually, in addition to each zone's apex. Each host is assigned to the zone whose name is its longest matching suffix; hosts outside every zone are logged and ignored. |
| `ZONE_REFRESH_INTERVAL` | No | `10m` | How often to re-list zones. Must be `>= 1m`. |
| `CF_ACCOUNT_TAG` | No | - | Optional account tag. Required for `cf_gateway_l7_requests_window`. In sharded deployments, set this on only one shard unless account queries are intentionally partitioned to avoid duplicate account-level metrics. |
| `ENABLED_QUERIES` | No | `http,firewall,gateway_l7` | Comma-separated query list. Allowed: `http`, `firewall`, `gateway_l7` (or alias `gateway`). Disable unsupported datasets (for example free-plan zones can use `http,gateway_l7`). |
| `POLL_INTERVAL` | No | `60s` | Must be `>= 30s`. |
| `WINDOW` | No | `1m` | Must be `>= POLL_INTERVAL` and `<= 15m`. |
| `PORT` | No | `8080` | HTTP listen port (binds `:PORT`). |
| `REQUEST_TIMEOUT` | No | `10s` | Per-request timeout to Cloudflare GraphQL. |
| `LOG_LEVEL` | No | `info` | `debug`, `info`, `warn`, or `error`. |

## Cloudflare API Token Setup

Create the token in the Cloudflare dashboard at [API Tokens](https://dash.cloudflare.com/profile/api-tokens) (`My Profile` -> `API Tokens`), then use `Create Token` -> `Custom token`.

Minimum token permissions:

- Zone discovery: `Zone` -> `Zone` -> `Read`.
- Zone-level metrics (`cf_http_*`, `cf_firewall_events_window`): `Zone` -> `Analytics` -> `Read` (`Analytics Read`).
- Account-level gateway metrics (`cf_gateway_l7_requests_window`, only when `CF_ACCOUNT_TAG` is set): `Account` -> `Account Analytics` -> `Read` (`Account Analytics Read`).

Resource scoping:

- Zone resources: `All zones from an account` lets new domains be picked up automatically. Scope to specific zones instead if you only want those polled.
- Account resources: scope to the specific account if you enable `CF_ACCOUNT_TAG`.

After creation, copy the token once and store it in Kubernetes Secret (`deploy/k8s/secret.example.yaml`) or your runtime secret manager.

## Local run

```bash
export CF_API_TOKEN="..."
# optional: hosts to report individually (zone apexes are always included)
export CF_HOSTS="docs.example.com,templates.example.com"
# optional: restrict to specific zones instead of all zones visible to the token
# export CF_ZONE_TAGS="zone-tag-1,zone-tag-2"
# optional, enables gateway L7 metrics
# export CF_ACCOUNT_TAG="..."
# optional, skip unsupported datasets such as firewall
# export ENABLED_QUERIES="http,gateway_l7"
make run
```

Validate metrics output:

```bash
curl -s http://localhost:8080/metrics | grep '^cf_'
```

## Build and test

```bash
make test
make build
```

## Docker

```bash
make docker-build
```

## Kubernetes

1. Create/update the token secret from `deploy/k8s/secret.example.yaml`.
2. Update `CF_HOSTS` and the image reference in `deploy/k8s/deployment.yaml`.
3. Keep a single replica (`replicas: 1`). Multiple replicas duplicate Cloudflare query load and emit duplicate metric series unless you intentionally shard by host/zone.
4. If you shard by zone/host across multiple deployments, avoid setting `CF_ACCOUNT_TAG` on more than one shard unless each shard queries a non-overlapping account slice. Otherwise `cf_gateway_l7_requests_window` will be duplicated.
5. Apply manifests:

```bash
kubectl apply -f deploy/k8s/secret.example.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
kubectl apply -f deploy/k8s/servicemonitor.yaml
```

After rollout, confirm Prometheus scrapes all three hosts (`example.com`, `docs.example.com`, `templates.example.com`) and wire Grafana panels/alerts to the exported `cf_` metrics.
