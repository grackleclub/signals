package signals

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

// newResource builds the Resource shared by all three providers — the join key
// SigNoz uses to correlate logs, metrics, and traces. See DESIGN.md "resource".
//
// service.name and deployment.environment.name use the pinned semconv keys
// (semconv.go); host.name comes from resource.WithHost and OTEL_RESOURCE_-
// ATTRIBUTES / OTEL_SERVICE_NAME from resource.WithFromEnv.
func newResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		keyServiceName.String(cfg.Service),
		keyDeployEnv.String(cfg.Env),
	}
	if cfg.Version != "" {
		attrs = append(attrs, keyServiceVersion.String(cfg.Version))
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
	return res, nil
}
