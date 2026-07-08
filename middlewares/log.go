package middlewares

import (
	"log/slog"
	"net/http"
	"time"
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

		slog.Info(
			"WebRequest",
			"proto", r.Proto,
			"method", r.Method,
			"url", r.URL,
			"duration", time.Since(startTime),
			"status", logRespWriter.statusCode,
			"traceId", r.Context().Value(ContextKey("traceId")))
	})
}
