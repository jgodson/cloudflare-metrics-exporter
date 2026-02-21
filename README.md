# Cloudflare Metrics Exporter

Prometheus exporter that polls Cloudflare GraphQL analytics every minute and publishes per-host windowed traffic metrics.

## Features

- Polls Cloudflare once per minute (default) with one in-flight poll at a time.
- Publishes `cf_http_*` per-host metrics and exporter health metrics.
- Exposes `/metrics`, `/healthz`, and `/readyz` on `:8080` by default.
- Includes Docker and Kubernetes deployment manifests.
- Standalone service with no database dependency.

## Metrics

- `cf_http_requests_window{zone,host}`
- `cf_http_edge_response_bytes_window{zone,host}`
- `cf_http_cached_requests_window{zone,host}`
- `cf_http_status_requests_window{zone,host,status_class}`
- `cf_firewall_events_window{zone,host,action}`
- `cf_gateway_l7_requests_window{account,host,action}` (when `CF_ACCOUNT_TAG` is set)
- `cf_exporter_query_up{query,target}` (`query` in `http`, `firewall`, `gateway_l7`; `target` is zone tag or account tag)
- `cf_exporter_query_errors_total{query,target}`
- `cf_exporter_up` (exporter process health; not query health)
- `cf_exporter_last_success_unixtime`
- `cf_exporter_poll_duration_seconds`
- `cf_exporter_poll_errors_total`

## Configuration

| Variable | Required | Default | Notes |
| --- | --- | --- | --- |
| `CF_API_TOKEN` | Yes | - | Cloudflare token with analytics read permissions. Store in a secret. |
| `CF_ZONE_TAGS` | Yes | - | Comma-separated Cloudflare zone IDs. |
| `CF_HOSTS` | Yes | - | Comma-separated hosts. |
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

- Zone-level metrics (`cf_http_*`, `cf_firewall_events_window`): `Zone` -> `Analytics` -> `Read` (`Analytics Read`).
- Account-level gateway metrics (`cf_gateway_l7_requests_window`, only when `CF_ACCOUNT_TAG` is set): `Account` -> `Account Analytics` -> `Read` (`Account Analytics Read`).

Resource scoping:

- Zone resources: scope to only the zones you put in `CF_ZONE_TAGS`.
- Account resources: scope to the specific account if you enable `CF_ACCOUNT_TAG`.

After creation, copy the token once and store it in Kubernetes Secret (`deploy/k8s/secret.example.yaml`) or your runtime secret manager.

## Local run

```bash
export CF_API_TOKEN="..."
export CF_ZONE_TAGS="zone-tag-1,zone-tag-2"
export CF_HOSTS="example.com,docs.example.com,templates.example.com"
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
2. Update `CF_ZONE_TAGS` and image reference in `deploy/k8s/deployment.yaml`.
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
