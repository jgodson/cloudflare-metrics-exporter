package cloudflare

type HostMetrics struct {
	Requests          float64
	EdgeResponseBytes float64
	CachedRequests    float64
	StatusRequests    map[string]float64
}
