package middlewares

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hbmartin/podcast-backend/metrics"
)

// LogResponseWriter captures the status code for request logging; a handler
// that never calls WriteHeader implicitly returns 200.
type LogResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *LogResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func newLogResponseWriter(w http.ResponseWriter) *LogResponseWriter {
	return &LogResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
}

func LogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()

		logRespWriter := newLogResponseWriter(w)
		next.ServeHTTP(logRespWriter, r)

		duration := time.Since(startTime)

		// r.Pattern is the matched mux route (bounded cardinality); requests
		// that never hit the mux (none in this chain) would be empty
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		metrics.HTTPRequestDuration.
			WithLabelValues(r.Method, route, strconv.Itoa(logRespWriter.statusCode)).
			Observe(duration.Seconds())

		slog.Info(
			"WebRequest",
			"proto", r.Proto,
			"method", r.Method,
			"url", r.URL,
			"duration", duration,
			"status", logRespWriter.statusCode,
			"traceId", r.Context().Value(ContextKey("traceId")))
	})
}
