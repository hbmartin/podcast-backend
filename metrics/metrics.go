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

// AttestEnrollments counts App Attest enrollment attempts by outcome
// (enrolled | rejected | error).
var AttestEnrollments = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "podcast_backend",
	Subsystem: "attest",
	Name:      "enrollments_total",
	Help:      "App Attest enrollment attempts by outcome.",
}, []string{"outcome"})

// AttestAssertions counts App Attest assertion verifications by endpoint and
// outcome (ok | unattested | invalid_key | bad_signature | stale | error).
var AttestAssertions = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "podcast_backend",
	Subsystem: "attest",
	Name:      "assertions_total",
	Help:      "App Attest assertion verifications by endpoint and outcome.",
}, []string{"endpoint", "outcome"})

// TranscriptContributions counts stored transcript contributions by engine.
var TranscriptContributions = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "podcast_backend",
	Subsystem: "transcripts",
	Name:      "contributions_total",
	Help:      "Stored transcript contributions by engine.",
}, []string{"engine"})

// TranscriptSightings counts transcript sightings by outcome
// (accepted | duplicate | rejected | fetched | fetch_failed).
var TranscriptSightings = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "podcast_backend",
	Subsystem: "transcripts",
	Name:      "sightings_total",
	Help:      "Transcript sightings by outcome.",
}, []string{"outcome"})

// TranscriptRejections counts transcript submissions rejected before storage
// by cause (vtt | fingerprint | duration | size | url | rate_limit).
var TranscriptRejections = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "podcast_backend",
	Subsystem: "transcripts",
	Name:      "rejections_total",
	Help:      "Transcript submissions rejected before storage by cause.",
}, []string{"cause"})
