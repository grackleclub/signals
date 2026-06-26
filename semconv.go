package signals

import semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

// This is the only file naming a semconv version. Everything else references
// these re-exported keys, so a version bump touches exactly one file.
//
// v1.41 emits deployment.environment.name (the key was deployment.environment
// until v1.27); the SigNoz collector's spanmetrics dimension must flip in
// lockstep. See DESIGN.md "semconv pinning".
var (
	keyServiceName    = semconv.ServiceNameKey               // "service.name"
	keyServiceVersion = semconv.ServiceVersionKey            // "service.version"
	keyDeployEnv      = semconv.DeploymentEnvironmentNameKey // "deployment.environment.name"
	keyHostName       = semconv.HostNameKey                  // "host.name"
)
