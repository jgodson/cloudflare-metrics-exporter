package cloudflare

// OtherHost is the host label used for zone traffic to hosts that are not tracked individually.
const OtherHost = "other"

type Zone struct {
	ID   string
	Name string
}

type HostMetrics struct {
	Requests          float64
	EdgeResponseBytes float64
	CachedRequests    float64
	StatusRequests    map[string]float64
}
