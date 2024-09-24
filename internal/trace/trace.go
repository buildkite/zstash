package trace

import (
	"context"
	"io"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var globalProvider *Provider
var tracer trace.Tracer

type Provider struct {
	tp *sdktrace.TracerProvider
}

func NewProvider(name string, exp sdktrace.SpanExporter) *Provider {
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	otel.SetTracerProvider(tp)

	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	tracer = tp.Tracer(name)

	globalProvider = &Provider{tp: tp}

	return globalProvider
}

func (p *Provider) Tracer(name string) trace.Tracer {
	return p.tp.Tracer(name)
}

func (p *Provider) Shutdown(ctx context.Context) error {
	return p.tp.Shutdown(ctx)
}

func Start(ctx context.Context, name string) (context.Context, trace.Span) {
	return tracer.Start(ctx, name)
}

func NewExporter(ctx context.Context) (sdktrace.SpanExporter, error) {

	exporter := os.Getenv("TRACE_EXPORTER")

	switch exporter {
	case "grpc":
		clientOTel := otlptracegrpc.NewClient()
		return otlptrace.New(ctx, clientOTel)
	case "stdout":
		return stdouttrace.New()
	default:
		return stdouttrace.New(stdouttrace.WithWriter(io.Discard))
	}
}
