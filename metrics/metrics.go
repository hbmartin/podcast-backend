// Package metrics exposes the server's Prometheus instruments. Everything is
// registered on the default registry and served by promhttp at GET /metrics.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// HTTPRequestDuration observes every routed request, labeled by method, the
// mux route pattern (not the raw path, to bound cardinality), and status.
var HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "podcast_backend",
	Subsystem: "http",
	Name:      "request_duration_seconds",
	Help:      "HTTP request latency by route pattern.",
	Buckets:   prometheus.DefBuckets,
}, []string{"method", "route", "status"})

// CrawlsTotal counts feed crawl attempts by outcome
// (ok | not_modified | failed).
var CrawlsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "podcast_backend",
	Subsystem: "crawler",
	Name:      "crawls_total",
	Help:      "Feed crawl attempts by outcome.",
}, []string{"outcome"})

// PushDeliveries counts APNs notification sends by outcome
// (delivered | failed | unregistered).
var PushDeliveries = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "podcast_backend",
	Subsystem: "push",
	Name:      "deliveries_total",
	Help:      "APNs notification deliveries by outcome.",
}, []string{"outcome"})
