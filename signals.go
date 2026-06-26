package signals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	otlpmetrichttp "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	logglobal "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// scopeName is this package's instrumentation scope (convention: import path).
const scopeName = "github.com/grackleclub/signals"

// Setup installs the global tracer, meter, and logger providers plus the W3C
// propagator, all sharing one Resource, and returns a ready slog.Logger and a
// shutdown that flushes/closes every exporter.
//
// Call once from main; defer the shutdown. Libraries must not call Setup —
// they obtain a tracer/meter from the installed globals via otel.Tracer(scope)
// / otel.Meter(scope).
//
// With no OTLP endpoint resolved (Config.Endpoint and the OTEL_* env vars all
// empty), Setup degrades to a console-only logger: no exporters, no error
// ("graceful off").
func Setup(ctx context.Context, cfg Config) (
	shutdown func(context.Context) error,
	logger *slog.Logger,
	err error,
) {
	cfg = cfg.resolved()

	res, err := newResource(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("signals setup: %w", err)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// closers run in reverse install order on shutdown.
	var closers []func(context.Context) error
	shutdown = func(ctx context.Context) error {
		var errs []error
		for i := len(closers) - 1; i >= 0; i-- {
			if err := closers[i](ctx); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	// Graceful off: nothing configured => console only, no exporters.
	if !cfg.otlpEnabled() {
		return shutdown, Logger(cfg, nil), nil
	}

	tp, err := tracerProvider(ctx, cfg, res)
	if err != nil {
		return nil, nil, fmt.Errorf("signals setup traces: %w", err)
	}
	otel.SetTracerProvider(tp)
	closers = append(closers, tp.Shutdown)

	mp, err := meterProvider(ctx, cfg, res)
	if err != nil {
		return nil, nil, fmt.Errorf("signals setup metrics: %w", err)
	}
	otel.SetMeterProvider(mp)
	closers = append(closers, mp.Shutdown)

	if !cfg.DisableRuntimeMetrics {
		if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
			return nil, nil, fmt.Errorf("signals setup runtime metrics: %w", err)
		}
	}

	lp, err := loggerProvider(ctx, cfg, res)
	if err != nil {
		return nil, nil, fmt.Errorf("signals setup logs: %w", err)
	}
	logglobal.SetLoggerProvider(lp)
	closers = append(closers, lp.Shutdown)

	return shutdown, Logger(cfg, lp), nil
}

// tracerProvider builds the OTLP/HTTP TracerProvider. The default sampler is
// ParentBased(AlwaysSample) — overridable via OTEL_TRACES_SAMPLER — so the
// SigNoz ingester sees complete traces to tail-sample. See DESIGN.md "sampling".
func tracerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	var opts []otlptracehttp.Option
	if host, insecure, path := endpointParts(cfg.Endpoint); host != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(host))
		if insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if path != "" {
			opts = append(opts, otlptracehttp.WithURLPath(path))
		}
	}
	if cfg.Token != "" {
		opts = append(opts, otlptracehttp.WithHeaders(bearer(cfg.Token)))
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exp),
	), nil
}

// meterProvider builds the OTLP/HTTP MeterProvider with a periodic reader (the
// SDK default interval, overridable via OTEL_METRIC_EXPORT_INTERVAL).
func meterProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	var opts []otlpmetrichttp.Option
	if host, insecure, path := endpointParts(cfg.Endpoint); host != "" {
		opts = append(opts, otlpmetrichttp.WithEndpoint(host))
		if insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		if path != "" {
			opts = append(opts, otlpmetrichttp.WithURLPath(path))
		}
	}
	if cfg.Token != "" {
		opts = append(opts, otlpmetrichttp.WithHeaders(bearer(cfg.Token)))
	}
	exp, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otlp metric exporter: %w", err)
	}
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)),
	), nil
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// endpointParts splits an OTLP endpoint into the host:port, whether it's plain
// HTTP (insecure), and any explicit base path. A path of "" lets the exporter
// append its default per-signal path (/v1/traces, /v1/metrics, /v1/logs); a
// non-empty endpoint with no scheme is treated as a bare host:port. host is
// empty only when raw is empty (the console-only case).
func endpointParts(raw string) (host string, insecure bool, path string) {
	if raw == "" {
		return "", false, ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw, false, "" // best effort: a bare host:port
	}
	path = u.Path
	if path == "/" {
		path = ""
	}
	return u.Host, u.Scheme == "http", path
}
