// Package telemetry wires OpenTelemetry tracing. It activates only when the
// standard OTEL_EXPORTER_OTLP_ENDPOINT (or ..._TRACES_ENDPOINT) environment
// variable is set, exporting spans over OTLP/HTTP; otherwise the app runs
// with the default no-op tracer.
package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Init configures the global TracerProvider from the standard OTEL_* env
// vars. It returns a shutdown func (always safe to call) and whether tracing
// was enabled.
func Init(ctx context.Context) (shutdown func(context.Context) error, enabled bool, err error) {
	noop := func(context.Context) error { return nil }

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return noop, false, nil
	}

	// resource.Default honors OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES;
	// give the service a sensible name when the deployment sets neither
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		os.Setenv("OTEL_SERVICE_NAME", "podcast-backend")
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop, false, fmt.Errorf("telemetry: creating OTLP exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.Default()),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return provider.Shutdown, true, nil
}
