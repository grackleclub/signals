package signals

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

// newResource builds the Resource shared by all three providers — the join key
// SigNoz uses to correlate logs, metrics, and traces.
//
// service.name and deployment.environment.name use the pinned semconv keys
// (semconv.go); host.name comes from resource.WithHost and OTEL_RESOURCE_-
// ATTRIBUTES / OTEL_SERVICE_NAME from resource.WithFromEnv.
func newResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{keyServiceName.String(cfg.Service)}
	if cfg.Version != "" {
		attrs = append(attrs, keyServiceVersion.String(cfg.Version))
	}
	// Only set deployment.environment.name from Config when given, so an empty
	// Env doesn't clobber a value from OTEL_RESOURCE_ATTRIBUTES.
	if cfg.Env != "" {
		attrs = append(attrs, keyDeployEnv.String(cfg.Env))
	}

	res, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithFromEnv(),
		resource.WithAttributes(attrs...),
	)
	// resource.New returns a usable resource alongside these non-fatal merge
	// errors (schema-URL mismatch between detectors, or a partially-failed
	// detector). Don't take down the whole service at startup for them.
	if err != nil &&
		!errors.Is(err, resource.ErrSchemaURLConflict) &&
		!errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("building resource: %w", err)
	}

	// Default the environment to "unknown" only when neither Config nor the env
	// supplied one. Config wins over env; env wins over this fallback.
	if _, ok := res.Set().Value(keyDeployEnv); !ok {
		res, err = resource.Merge(res, resource.NewSchemaless(keyDeployEnv.String("unknown")))
		if err != nil {
			return nil, fmt.Errorf("defaulting environment: %w", err)
		}
	}
	return res, nil
}
