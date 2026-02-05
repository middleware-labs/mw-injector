---
name: otel-expert
description: |
  OpenTelemetry expertise covering semantic conventions, attribute naming, instrumentation patterns, and OTEL compliance.
  Use when: (1) Naming attributes or metrics following OTEL conventions, (2) Implementing tracing or metrics,
  (3) Configuring OTEL exporters or propagators, (4) Reviewing code for OTEL compliance,
  (5) Questions about spans, traces, or telemetry data structures.
---

# OpenTelemetry Expert Guide

## Semantic Conventions

Use standardized attribute names for interoperability.

### Process Attributes
```
process.pid                 - Process ID
process.parent_pid          - Parent process ID
process.executable.name     - Executable name (e.g., "java")
process.executable.path     - Full path to executable
process.command             - Full command used to launch
process.command_args        - Command arguments as array
process.command_line        - Full command line string
process.owner               - Username running the process
process.runtime.name        - Runtime name (e.g., "OpenJDK Runtime")
process.runtime.version     - Runtime version
process.runtime.description - Runtime description
```

### Service Attributes
```
service.name                - Logical service name (REQUIRED)
service.version             - Service version
service.namespace           - Service namespace
service.instance.id         - Unique instance identifier
```

### Container Attributes
```
container.id                - Full container ID
container.name              - Container name
container.runtime           - Runtime (docker, containerd, etc.)
container.image.name        - Image name
container.image.tag         - Image tag
```

### Host Attributes
```
host.id                     - Unique host ID
host.name                   - Hostname
host.type                   - Host type (e.g., "vm")
host.arch                   - Architecture (e.g., "amd64")
```

## Environment Variables

### Required
```bash
OTEL_SERVICE_NAME=my-service        # Service identifier
```

### Common Configuration
```bash
OTEL_TRACES_EXPORTER=otlp           # Exporter type (otlp, jaeger, zipkin, none)
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc    # grpc or http/protobuf

OTEL_TRACES_SAMPLER=parentbased_always_on
OTEL_PROPAGATORS=tracecontext,baggage,b3

OTEL_RESOURCE_ATTRIBUTES=service.namespace=prod,deployment.environment=production
```

### Java Agent Specific
```bash
JAVA_TOOL_OPTIONS=-javaagent:/path/to/opentelemetry-javaagent.jar
OTEL_JAVAAGENT_ENABLED=true
OTEL_INSTRUMENTATION_COMMON_DEFAULT_ENABLED=true
```

## Propagators

Context propagation formats:

| Propagator | Header | Use Case |
|------------|--------|----------|
| tracecontext | traceparent, tracestate | W3C standard (default) |
| b3 | X-B3-TraceId, X-B3-SpanId | Zipkin compatibility |
| b3multi | X-B3-* (multiple headers) | Legacy Zipkin |
| baggage | baggage | W3C baggage |
| jaeger | uber-trace-id | Jaeger native |

Middleware.io default: `b3`

## Trace Structure

```
Trace
└── Span (root)
    ├── Span (child)
    │   └── Span (grandchild)
    └── Span (child)

Span contains:
- trace_id (16 bytes)
- span_id (8 bytes)
- parent_span_id (8 bytes, optional)
- name (operation name)
- kind (CLIENT, SERVER, PRODUCER, CONSUMER, INTERNAL)
- start_time, end_time
- attributes (key-value pairs)
- events (timestamped logs)
- status (OK, ERROR)
- links (to other spans)
```

## Resource Attributes in Code

```go
import "go.opentelemetry.io/otel/sdk/resource"
import semconv "go.opentelemetry.io/otel/semconv/v1.24.0"

res, _ := resource.New(ctx,
    resource.WithAttributes(
        semconv.ServiceName("my-service"),
        semconv.ServiceVersion("1.0.0"),
        semconv.ProcessPID(os.Getpid()),
        semconv.HostName(hostname),
    ),
)
```

## Attribute Naming Rules

1. Use lowercase with dots as separators: `service.name`, not `serviceName`
2. Use semantic convention names when they exist
3. Prefix custom attributes with domain: `mycompany.custom_attr`
4. Use consistent types (string, int, float, bool, arrays)
5. Avoid high-cardinality values in attribute names

## Middleware.io Specific

Environment variables used by mw-injector:

```bash
MW_API_KEY              # Authentication key
MW_TARGET               # Endpoint (default: https://prod.middleware.io:443)
MW_SERVICE_NAME         # Service name (maps to OTEL_SERVICE_NAME)

MW_APM_COLLECT_TRACES=true
MW_APM_COLLECT_METRICS=true
MW_APM_COLLECT_LOGS=true
MW_APM_COLLECT_PROFILING=true

MW_PROPAGATORS=b3       # Default propagator
MW_PROFILING_ALLOC=512k
MW_PROFILING_LOCK=10ms
```

## Compliance Checklist

When implementing OTEL-compliant code:

- [ ] Service name is always set
- [ ] Use semantic convention attribute names
- [ ] Trace context propagates correctly
- [ ] Spans have meaningful names (verb + noun)
- [ ] Errors set span status to ERROR
- [ ] High-cardinality data goes in events, not attributes
- [ ] Resource attributes set at initialization
