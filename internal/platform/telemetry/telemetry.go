// Package telemetry provides optional OpenTelemetry tracing for service
// processes. Telemetry is disabled unless an explicit OTLP endpoint is set.
package telemetry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// ValidateEndpoint accepts HTTPS OTLP endpoints and loopback HTTP endpoints
// for local collectors. Query strings and fragments are rejected so endpoint
// configuration cannot smuggle exporter options or credentials.
func ValidateEndpoint(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		(endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return errors.New("OTLP endpoint must be an HTTP(S) URL without credentials, query, or fragment")
	}
	if endpoint.Scheme == "http" && !isLoopbackHost(endpoint.Hostname()) {
		return errors.New("plaintext OTLP endpoints are limited to loopback development")
	}
	return nil
}

// Setup installs a batch OTLP HTTP exporter when endpoint is configured. The
// returned shutdown function flushes pending spans and restores no process
// state; callers should invoke it during graceful shutdown.
func Setup(ctx context.Context, serviceName, endpoint string) (func(context.Context) error, error) {
	endpoint = strings.TrimSpace(endpoint)
	if err := ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("parse OTLP endpoint")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1/traces"
	}
	options := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(parsed.String())}
	if parsed.Scheme == "http" {
		options = append(options, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, errors.New("initialize OTLP trace exporter")
	}
	resource, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(serviceName),
			attribute.String("voiceasset.telemetry", "otlp-http"),
		),
	)
	if err != nil {
		return nil, errors.New("initialize telemetry resource")
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return provider.Shutdown, nil
}

// HTTPMiddleware instruments inbound HTTP requests and propagates W3C trace
// context. Operation names use the bounded service label, never raw URLs.
func HTTPMiddleware(serviceName string, next http.Handler) http.Handler {
	tracer := otel.Tracer(serviceName)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/metrics" || request.URL.Path == "/api/v1/realtime/transcriptions" {
			next.ServeHTTP(response, request)
			return
		}
		parent := otel.GetTextMapPropagator().Extract(request.Context(), propagation.HeaderCarrier(request.Header))
		ctx, span := tracer.Start(parent, serviceName, oteltrace.WithSpanKind(oteltrace.SpanKindServer))
		defer span.End()
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
