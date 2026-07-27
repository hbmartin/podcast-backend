// Package metrics exposes the server's Prometheus instruments. Everything is
// registered on the default registry and served by promhttp at GET /metrics.
package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

var meter = otel.Meter("github.com/hbmartin/podcast-backend")
var otelHTTPRequestDuration, _ = meter.Float64Histogram("podcast_backend.http.request_duration_seconds")
var otelRoleHeartbeat, _ = meter.Int64Gauge("podcast_backend.role.heartbeat_unixtime")
var otelQueueAge, _ = meter.Float64Gauge("podcast_backend.queue.oldest_task_age_seconds")
var otelSchedulerSuccess, _ = meter.Int64Gauge("podcast_backend.scheduler.last_success_unixtime")

// HTTPRequestDuration observes every routed request, labeled by method, the
// mux route pattern (not the raw path, to bound cardinality), and status.
var HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "podcast_backend",
	Subsystem: "http",
	Name:      "request_duration_seconds",
	Help:      "HTTP request latency by route pattern.",
	Buckets:   prometheus.DefBuckets,
}, []string{"method", "route", "status"})

func ObserveHTTPRequest(ctx context.Context, method, route, status string, seconds float64) {
	HTTPRequestDuration.WithLabelValues(method, route, status).Observe(seconds)
	otelHTTPRequestDuration.Record(ctx, seconds, otelmetric.WithAttributes(
		attribute.String("http.request.method", method),
		attribute.String("http.route", route),
		attribute.String("http.response.status_code", status),
	))
}

var RoleHeartbeat = promauto.NewGaugeVec(prometheus.GaugeOpts{Namespace: "podcast_backend", Subsystem: "role", Name: "heartbeat_unixtime", Help: "Last heartbeat Unix timestamp for each process role."}, []string{"role"})
var QueueOldestTaskAge = promauto.NewGauge(prometheus.GaugeOpts{Namespace: "podcast_backend", Subsystem: "queue", Name: "oldest_task_age_seconds", Help: "Age of the oldest pending task across queues."})
var SchedulerLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{Namespace: "podcast_backend", Subsystem: "scheduler", Name: "last_success_unixtime", Help: "Last successful scheduling timestamp."}, []string{"scheduler"})
var ReadinessChecks = promauto.NewCounterVec(prometheus.CounterOpts{Namespace: "podcast_backend", Subsystem: "readiness", Name: "checks_total", Help: "Readiness checks by outcome."}, []string{"outcome"})

func RecordRoleHeartbeat(ctx context.Context, role string) {
	now := time.Now().Unix()
	RoleHeartbeat.WithLabelValues(role).Set(float64(now))
	otelRoleHeartbeat.Record(ctx, now, otelmetric.WithAttributes(attribute.String("process.role", role)))
}
func RecordQueueAge(ctx context.Context, seconds float64) {
	QueueOldestTaskAge.Set(seconds)
	otelQueueAge.Record(ctx, seconds)
}
func RecordSchedulerSuccess(ctx context.Context, scheduler string) {
	now := time.Now().Unix()
	SchedulerLastSuccess.WithLabelValues(scheduler).Set(float64(now))
	otelSchedulerSuccess.Record(ctx, now, otelmetric.WithAttributes(attribute.String("scheduler", scheduler)))
}

// CrawlsTotal counts feed crawl attempts by outcome
// (ok | not_modified | failed).
var CrawlsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "podcast_backend",
	Subsystem: "crawler",
	Name:      "crawls_total",
	Help:      "Feed crawl attempts by outcome.",
}, []string{"outcome"})

// GroupFanoutDropped counts group-post notification fan-outs dropped because
// the queue-less direct path hit its concurrency limit. A nonzero rate means
// members silently missed new-post pushes; deploy a task queue or raise the
// direct-path limit.
var GroupFanoutDropped = promauto.NewCounter(prometheus.CounterOpts{
	Namespace: "podcast_backend",
	Subsystem: "push",
	Name:      "group_fanout_dropped_total",
	Help:      "Group post notification fan-outs dropped at the direct-path concurrency limit.",
})

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
