package signals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

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

	oc, err := cfg.otlp()
	if err != nil {
		return nil, nil, fmt.Errorf("signals setup: %w", err)
	}

	// On any failure past here, flush/close what's already installed before
	// returning — a failed Setup must not leak exporters or batch goroutines.
	tp, err := tracerProvider(ctx, oc, res)
	if err != nil {
		_ = shutdown(ctx)
		return nil, nil, fmt.Errorf("signals setup traces: %w", err)
	}
	otel.SetTracerProvider(tp)
	closers = append(closers, tp.Shutdown)

	mp, err := meterProvider(ctx, oc, res)
	if err != nil {
		_ = shutdown(ctx)
		return nil, nil, fmt.Errorf("signals setup metrics: %w", err)
	}
	otel.SetMeterProvider(mp)
	closers = append(closers, mp.Shutdown)

	if !cfg.DisableRuntimeMetrics {
		if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
			_ = shutdown(ctx)
			return nil, nil, fmt.Errorf("signals setup runtime metrics: %w", err)
		}
	}

	lp, err := loggerProvider(ctx, oc, res)
	if err != nil {
		_ = shutdown(ctx)
		return nil, nil, fmt.Errorf("signals setup logs: %w", err)
	}
	logglobal.SetLoggerProvider(lp)
	closers = append(closers, lp.Shutdown)

	return shutdown, Logger(cfg, lp), nil
}

// tracerProvider builds the OTLP/HTTP TracerProvider. The default sampler is
// ParentBased(AlwaysSample) — overridable via OTEL_TRACES_SAMPLER — so the
// SigNoz ingester sees complete traces to tail-sample. See DESIGN.md "sampling".
func tracerProvider(ctx context.Context, oc otlpConfig, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	var opts []otlptracehttp.Option
	if oc.host != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(oc.host))
		if oc.insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if oc.path != "" {
			opts = append(opts, otlptracehttp.WithURLPath(oc.path))
		}
	}
	if len(oc.headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(oc.headers))
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
func meterProvider(ctx context.Context, oc otlpConfig, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	var opts []otlpmetrichttp.Option
	if oc.host != "" {
		opts = append(opts, otlpmetrichttp.WithEndpoint(oc.host))
		if oc.insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		if oc.path != "" {
			opts = append(opts, otlpmetrichttp.WithURLPath(oc.path))
		}
	}
	if len(oc.headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(oc.headers))
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

// otlpConfig is the OTLP/HTTP transport resolved once from Config and shared by
// all three exporters. A zero host means "no explicit endpoint" — the exporters
// then read OTEL_EXPORTER_OTLP[_*]_ENDPOINT natively (per-signal precedence,
// default /v1/{signal} paths). nil headers likewise defer to the SDK's env.
type otlpConfig struct {
	host     string
	insecure bool
	path     string
	headers  map[string]string
}

// otlp resolves the OTLP transport from Config. An explicit Config.Endpoint is
// validated and split into host/insecure/path (and, being explicit, overrides
// the env). A Token becomes a bearer header merged over OTEL_EXPORTER_OTLP_-
// HEADERS so both are honored rather than one clobbering the other.
func (c Config) otlp() (otlpConfig, error) {
	var oc otlpConfig
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil {
			return otlpConfig{}, fmt.Errorf("parsing endpoint %q: %w", c.Endpoint, err)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return otlpConfig{}, fmt.Errorf(
				"endpoint %q must be an http(s) URL with a host", c.Endpoint)
		}
		oc.host = u.Host
		oc.insecure = u.Scheme == "http"
		if oc.path = u.Path; oc.path == "/" {
			oc.path = ""
		}
	}
	if c.Token != "" {
		oc.headers = envHeaders()
		oc.headers["Authorization"] = "Bearer " + c.Token
	}
	return oc, nil
}

// envHeaders parses OTEL_EXPORTER_OTLP_HEADERS ("k1=v1,k2=v2") so a bearer Token
// can be merged with caller-set headers instead of replacing them.
func envHeaders() map[string]string {
	h := map[string]string{}
	for kv := range strings.SplitSeq(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok || k == "" {
			continue
		}
		h[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return h
}
