// Package telemetry wires up the OpenTelemetry SDK for traces and metrics,
// plus a Prometheus scrape endpoint fed from the same metric pipeline. It is
// the only package in go-api that talks to the OTel SDK directly — other
// packages (middleware, logging) consume the Tracer/Meter/Fielder it
// produces without depending on the SDK themselves.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"go.opentelemetry.io/otel"
	otlpmetricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlptracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// Config controls what telemetry pipelines are built. Zero-value Config is
// safe: it yields a no-op tracer, a Prometheus-only meter, and no OTLP
// export.
type Config struct {
	ServiceName string
	Env         string

	// TracingEnabled turns on the OTLP trace exporter + SDK TracerProvider.
	// When false, the global no-op tracer is used (spans are free but inert).
	TracingEnabled bool
	// SampleRatio is the fraction of traces sampled when TracingEnabled,
	// applied via a parent-based ratio sampler (always samples if the
	// parent context is already sampled). 1.0 = sample everything.
	SampleRatio float64

	// MetricsEnabled turns on the Prometheus exporter (always) and, if
	// OTLPEndpoint is also set, an additional OTLP metric exporter.
	MetricsEnabled bool

	// OTLPEndpoint is a host:port gRPC endpoint, e.g. "otel-collector:4317".
	// Empty disables OTLP export (traces are then dropped even if
	// TracingEnabled is true — set an endpoint to actually ship them).
	OTLPEndpoint string
	OTLPInsecure bool
}

// Provider holds every telemetry handle a service needs after setup.
type Provider struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Tracer         trace.Tracer
	Meter          metric.Meter
	Propagator     propagation.TextMapPropagator

	// MetricsHandler serves the Prometheus text exposition format; mount it
	// at /metrics (go-api's health package does this for you).
	MetricsHandler http.Handler

	shutdown []func(context.Context) error
}

// New builds the telemetry pipelines described by cfg and registers them as
// the process-wide OTel defaults (otel.SetTracerProvider /
// otel.SetTextMapPropagator) so that any third-party instrumentation that
// reads the globals also picks them up.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.DeploymentEnvironmentNameKey.String(cfg.Env),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	otel.SetTextMapPropagator(propagator)

	p := &Provider{Propagator: propagator}

	if err := p.setupTracing(ctx, cfg, res); err != nil {
		return nil, err
	}
	if err := p.setupMetrics(ctx, cfg, res); err != nil {
		return nil, err
	}

	p.Tracer = p.TracerProvider.Tracer(cfg.ServiceName)
	p.Meter = p.MeterProvider.Meter(cfg.ServiceName)

	return p, nil
}

func (p *Provider) setupTracing(ctx context.Context, cfg Config, res *resource.Resource) error {
	if !cfg.TracingEnabled || cfg.OTLPEndpoint == "" {
		tp := trace.NewNoopTracerProvider()
		p.TracerProvider = tp
		otel.SetTracerProvider(tp)
		return nil
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.OTLPInsecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return fmt.Errorf("telemetry: otlp trace exporter: %w", err)
	}

	ratio := cfg.SampleRatio
	if ratio <= 0 {
		ratio = 1.0
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)
	p.TracerProvider = tp
	p.shutdown = append(p.shutdown, tp.Shutdown)
	otel.SetTracerProvider(tp)
	return nil
}

func (p *Provider) setupMetrics(ctx context.Context, cfg Config, res *resource.Resource) error {
	registry := prometheus.NewRegistry()
	promExporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return fmt.Errorf("telemetry: prometheus exporter: %w", err)
	}
	p.MetricsHandler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	readers := []sdkmetric.Option{sdkmetric.WithResource(res), sdkmetric.WithReader(promExporter)}

	if cfg.MetricsEnabled && cfg.OTLPEndpoint != "" {
		gopts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint)}
		if cfg.OTLPInsecure {
			gopts = append(gopts, otlpmetricgrpc.WithInsecure())
		}
		otlpExporter, err := otlpmetricgrpc.New(ctx, gopts...)
		if err != nil {
			return fmt.Errorf("telemetry: otlp metric exporter: %w", err)
		}
		readers = append(readers, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(otlpExporter)))
	}

	mp := sdkmetric.NewMeterProvider(readers...)
	p.MeterProvider = mp
	p.shutdown = append(p.shutdown, mp.Shutdown)
	otel.SetMeterProvider(mp)
	return nil
}

// Shutdown flushes and closes every exporter. Call it once during graceful
// shutdown, after the HTTP server has stopped accepting new requests.
func (p *Provider) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, fn := range p.shutdown {
		if err := fn(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Extract reads a propagated trace context out of HTTP headers.
func (p *Provider) Extract(ctx context.Context, header http.Header) context.Context {
	return p.Propagator.Extract(ctx, propagation.HeaderCarrier(header))
}

// Inject writes the trace context from ctx into HTTP headers, for outbound
// calls (e.g. via an instrumented http.Client).
func (p *Provider) Inject(ctx context.Context, header http.Header) {
	p.Propagator.Inject(ctx, propagation.HeaderCarrier(header))
}

// TraceLogFields is a logging.Fielder: it attaches trace_id/span_id to log
// records whenever ctx carries an active, valid span context, so logs and
// traces can be correlated in any backend that indexes both.
func TraceLogFields(ctx context.Context) []slog.Attr {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.HasTraceID() {
		return nil
	}
	attrs := []slog.Attr{slog.String("trace_id", sc.TraceID().String())}
	if sc.HasSpanID() {
		attrs = append(attrs, slog.String("span_id", sc.SpanID().String()))
	}
	return attrs
}
