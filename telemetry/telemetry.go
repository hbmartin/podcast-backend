package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func Init(ctx context.Context) (shutdown func(context.Context) error, tracingEnabled bool, err error) {
	noop := func(context.Context) error { return nil }
	genericEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	traceConfigured := genericEndpoint != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
	metricConfigured := genericEndpoint != "" || os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != ""
	if !traceConfigured && !metricConfigured {
		slog.Warn("OpenTelemetry export is not configured; continuing without Grafana Cloud export")
		return noop, false, nil
	}
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		_ = os.Setenv("OTEL_SERVICE_NAME", "podcast-backend")
	}
	res := resource.Default()
	var shutdowns []func(context.Context) error
	if traceConfigured {
		exporter, exportErr := otlptracehttp.New(ctx)
		if exportErr != nil {
			return noop, false, fmt.Errorf("telemetry: creating OTLP trace exporter: %w", exportErr)
		}
		provider := sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(2048), sdktrace.WithMaxExportBatchSize(256), sdktrace.WithExportTimeout(5*time.Second)),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(provider)
		shutdowns = append(shutdowns, provider.Shutdown)
		tracingEnabled = true
	} else {
		slog.Warn("OpenTelemetry trace export is not configured")
	}
	if metricConfigured {
		exporter, exportErr := otlpmetrichttp.New(ctx)
		if exportErr != nil {
			return noop, tracingEnabled, fmt.Errorf("telemetry: creating OTLP metric exporter: %w", exportErr)
		}
		provider := metric.NewMeterProvider(metric.WithResource(res), metric.WithReader(metric.NewPeriodicReader(exporter, metric.WithInterval(30*time.Second), metric.WithTimeout(5*time.Second))))
		otel.SetMeterProvider(provider)
		shutdowns = append(shutdowns, provider.Shutdown)
	} else {
		slog.Warn("OpenTelemetry metric export is not configured")
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return func(shutdownCtx context.Context) error {
		var combined error
		for i := len(shutdowns) - 1; i >= 0; i-- {
			combined = errors.Join(combined, shutdowns[i](shutdownCtx))
		}
		return combined
	}, tracingEnabled, nil
}
