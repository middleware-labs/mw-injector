# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

MW Injector is a Go library for process discovery and auto-instrumentation on Linux hosts, used by the Middleware.io observability platform. It discovers Java/Node/Python applications across host processes, Docker containers, Tomcat deployments, and systemd services, and instruments them with OpenTelemetry agents.

Imported by **mw-agent** via `pkg/otelinject` — not used as a standalone CLI.

**Module:** `github.com/middleware-labs/java-injector`
**Go Version:** 1.24.2

## Build and Test Commands

```bash
# Build all packages
go build ./...

# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./pkg/discovery

# Run specific test
go test -v ./pkg/discovery -run TestParseCommandLine
```

## Architecture

### Package Structure

- **pkg/discovery/** - Process discovery engine with concurrent worker pool; finds Java/Node/Python processes
- **pkg/otelinject/** - OTel injection interface; the primary integration point for mw-agent
- **pkg/agent/** - Java agent installation, validation, and permission management
- **pkg/systemd/** - Creates drop-in files for systemd services; handles Tomcat CATALINA_OPTS
- **pkg/docker/** - Container instrumentation and state tracking
- **pkg/config/** - Middleware.io environment variable configuration (~20 MW_* env vars)
- **pkg/naming/** - Service name generation with sanitization rules
- **pkg/state/** - JSON-based state persistence for tracking instrumentation

### Key Patterns

**Discovery Pipeline:** Worker pool pattern with configurable concurrency (default: 10 workers). Context-based cancellation. Entry points in `pkg/discovery/discovery.go`:
- `FindAllJavaProcesses(ctx)`, `FindInstrumentedProcesses(ctx)`, `FindMiddlewareProcesses(ctx)`
- `FindAllNodeProcesses(ctx)`, `FindAllPythonProcess(ctx)`

**Service Name Resolution:** 6-level heuristic in `pkg/discovery/service.go`:
1. Container name → 2. OTEL_SERVICE_NAME env → 3. Systemd unit name → 4. Java system properties → 5. JAR filename → 6. Directory structure

**Systemd Integration:** Drop-in files created at `/etc/systemd/system/{service}.service.d/10-middleware.conf` using JAVA_TOOL_OPTIONS for standard Java or CATALINA_OPTS for Tomcat.

**State Persistence:** JSON files at `/etc/middleware/state/` track instrumented services to enable clean uninstrumentation.

### Key Data Structures

- `JavaProcess` (pkg/discovery/discovery.go) - OTEL-compliant process representation with ~50 fields
- `ProcessConfiguration` (pkg/config/config.go) - Middleware.io env var configuration
- `DiscoveryOptions` (pkg/discovery/discovery.go) - Controls concurrency, timeout, filtering

### Default Paths

| Component | Path |
|-----------|------|
| Java Agent | `/opt/middleware/agents/middleware-javaagent-1.8.1.jar` |
| State Files | `/etc/middleware/state/` |
| Config Files | `/etc/middleware/services/` |
| Docker State | `/etc/middleware/docker/instrumented.json` |

## Adding New Features

- **New process type:** Create `{type}_process.go` in `pkg/discovery/`
- **New config field:** Extend `ProcessConfiguration` in `pkg/config/config.go`
