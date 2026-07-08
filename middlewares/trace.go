package middlewares

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

type ContextKey string

// TraceMiddleware stamps every request with a traceId for log correlation.
// When OpenTelemetry tracing is active (the otelhttp wrapper started a span,
// honoring any inbound W3C traceparent), the span's trace id is reused so
// logs and exported spans correlate; otherwise a fresh UUID is minted.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var traceId string
		if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
			traceId = sc.TraceID().String()
		} else {
			traceId = uuid.New().String()
		}

		ctx := context.WithValue(r.Context(), ContextKey("traceId"), traceId)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
