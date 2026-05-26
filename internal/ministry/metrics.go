package ministry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metric vectors used by the ministry client. Registered to the default
// Prometheus registry the first time this package is imported, so /metrics
// reflects them automatically.
var (
	apiCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ministry_api_calls_total",
		Help: "Total upstream calls to the Hellenic Ministry API.",
	}, []string{"endpoint", "outcome"})

	apiLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ministry_api_latency_seconds",
		Help:    "Upstream call latency by endpoint.",
		Buckets: prometheus.DefBuckets,
	}, []string{"endpoint"})

	cacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ministry_cache_hits_total",
		Help: "Cache lookups served from local TTL cache (no upstream call).",
	}, []string{"key"})

	cacheMisses = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ministry_cache_misses_total",
		Help: "Cache lookups that fell through to upstream.",
	}, []string{"key"})

	apiRetries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ministry_api_retries_total",
		Help: "Retry attempts triggered by a retryable upstream status.",
	}, []string{"endpoint"})
)

// observeCacheHit / observeCacheMiss are thin wrappers used at the call sites.
func observeCacheHit(key string)  { cacheHits.WithLabelValues(key).Inc() }
func observeCacheMiss(key string) { cacheMisses.WithLabelValues(key).Inc() }
