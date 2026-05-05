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

# Run handler-specific tests
go test -v ./pkg/discovery -run TestJavaHandler
go test -v ./pkg/discovery -run TestNodeHandler
go test -v ./pkg/discovery -run TestPythonHandler
go test -v ./pkg/discovery -run TestHandlerRegistry
```

## Architecture

### Package Structure

- **pkg/discovery/** — Process discovery engine with registry-driven language handlers
  - `discovery.go` — Core pipeline: scan → classify → enrich → filter (worker pool, 10 workers default)
  - `registry.go` — `LanguageHandler` interface + `HandlerRegistry` (first-match-wins)
  - `java_handler.go` — Java handler: detection, enrichment, filtering, service name helpers
  - `node_handler.go` — Node.js handler: same pattern + Node-specific lookup tables
  - `python_handler.go` — Python handler: same pattern + PythonAgentType definitions
  - `process.go` — Unified `Process` struct (OTel semantic conventions + `Details` map)
  - `types.go` — `Language` enum, `IntegrationInspector`/`IntegrationRegistry`
  - `scanner.go` — /proc enumeration into `ProcessInfo` structs
  - `proc_readers.go` — /proc metadata readers (owner, status, create time, ppid)
  - `service_name.go` — Service name extraction and sanitization (shared across handlers)
  - `service.go` — Systemd unit helpers (cgroup parsing, ignored unit list)
  - `instrumentation.go` — `AgentType`, `ServiceSetting`, LD_PRELOAD detection
  - `report.go` — `GetAgentReportValue()` for backend reporting
  - `cache.go` — Process metadata cache (keyed by PID + create time, 20-min TTL)
  - `container.go` — Container detection via /proc cgroup + mountinfo
  - `docker.go` — Docker daemon queries for container-aware discovery
  - `tomcat.go` — Tomcat-specific webapp scanning
- **pkg/otelinject/** — OTel injection; primary integration point for mw-agent
  - `interfaces.go` — `Language` alias (= discovery.Language), `OtelInjector` interface
  - `strategy.go` — `InstrumentationStrategy` interface + `StrategyRegistry`
  - `systemd_strategy.go` — `SystemdDropinStrategy` (instruments via systemd drop-in files)
  - `dropin.go` — Drop-in file creation/removal at `/etc/systemd/system/{unit}.service.d/`
  - `systemd_api.go` — High-level API: `InstrumentUnit()`, `ListSystemdServices()`, `ReportStatus()`
  - `injector_java.go` — `JavaSystemdInjector` (OtelInjector for Java)
  - `injector_node.go` — `NodeSystemdInjector` (OtelInjector for Node)
  - `injector_python.go` — `PythonSystemdInjector` (OtelInjector for Python)
  - `validate.go` — Agent asset validation + libc flavor detection
- **pkg/systemd/** — Systemd service management (status, restart, drop-in cleanup)
- **pkg/agent/** — Java agent installation, validation, and permission management
- **pkg/docker/** — Container instrumentation and state tracking
- **pkg/config/** — Middleware.io environment variable configuration (~20 MW_* env vars)
- **pkg/naming/** — Service name generation with sanitization rules
- **pkg/state/** — JSON-based state persistence for tracking instrumentation
- **pkg/reporter/** — Backend API reporting client

### Key Patterns

**Discovery Pipeline:** Registry-driven with `LanguageHandler` interface. Each handler implements Detect → Enrich → PassesFilter → ToServiceSetting. Entry points:
- `FindAllProcesses(ctx)` — discovers all supported language processes
- `FindProcessesByLanguage(ctx, lang)` — discovers processes for a specific language

**Instrumentation Strategy:** Pluggable via `InstrumentationStrategy` interface. Strategies are tried in registration order; `CanHandle()` determines applicability. Currently only `SystemdDropinStrategy` is registered.

**Service Name Resolution:** 6-level heuristic (varies by language, implemented in each handler):
1. Container name → 2. OTEL_SERVICE_NAME env → 3. Systemd unit name → 4. Language-specific properties → 5. Entry point filename → 6. Directory structure

**Systemd Integration:** Drop-in files created at `/etc/systemd/system/{service}.service.d/middleware-otel.conf` using LD_PRELOAD for Java/Node or LD_PRELOAD + PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX for Python.

**State Persistence:** JSON files at `/etc/middleware/state/` track instrumented services to enable clean uninstrumentation.

### Key Data Structures

- `Process` (pkg/discovery/process.go) — Unified process representation with OTel fields + `Details map[string]any`
- `LanguageHandler` (pkg/discovery/registry.go) — Interface for language-specific pipeline phases
- `InstrumentationStrategy` (pkg/otelinject/strategy.go) — Interface for pluggable instrumentation methods
- `ServiceSetting` (pkg/discovery/instrumentation.go) — Backend reporting payload per service
- `ProcessConfiguration` (pkg/config/config.go) — Middleware.io env var configuration
- `DiscoveryOptions` (pkg/discovery/discovery.go) — Controls concurrency, timeout, filtering

### Default Paths

| Component | Path |
|-----------|------|
| Java Agent | `/opt/middleware/agents/middleware-javaagent-1.8.1.jar` |
| State Files | `/etc/middleware/state/` |
| Config Files | `/etc/middleware/services/` |
| Docker State | `/etc/middleware/docker/instrumented.json` |
| Systemd Drop-ins | `/etc/systemd/system/{unit}.service.d/middleware-otel.conf` |

## Adding New Features

- **New language:** Implement `LanguageHandler` in `pkg/discovery/{lang}_handler.go`, register in `NewHandlerRegistry()`. No changes to core pipeline.
- **New instrumentation method:** Implement `InstrumentationStrategy` in `pkg/otelinject/{method}_strategy.go`, register in `NewStrategyRegistry()`.
- **New config field:** Extend `ProcessConfiguration` in `pkg/config/config.go`
