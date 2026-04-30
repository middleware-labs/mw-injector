# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

MW Injector is a Go library for process discovery and auto-instrumentation on Linux hosts, used by the Middleware.io observability platform. It discovers Java/Node/Python applications across host processes, Docker/Podman containers, Tomcat deployments, and systemd services, and instruments them via systemd drop-ins or OBI (OpenTelemetry eBPF Instrumentation).

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

# OBI config tests
go test -v ./pkg/otelinject -run TestOBI
```

## Architecture

### Package Structure

- **pkg/discovery/** — Process discovery engine with registry-driven language handlers
  - `discovery.go` — Core pipeline: scan → classify → enrich → filter (worker pool, 10 workers default)
  - `registry.go` — `LanguageHandler` interface + `HandlerRegistry` (first-match-wins)
  - `java_handler.go` — Java handler: detection, enrichment, filtering, service name helpers
  - `node_handler.go` — Node.js handler: same pattern + Node-specific lookup tables
  - `python_handler.go` — Python handler: same pattern + PythonAgentType definitions
  - `process.go` — Unified `Process` struct (OTel semantic conventions + `Details` map + `Fingerprint()`)
  - `types.go` — `Language` enum, `IntegrationInspector`/`IntegrationRegistry`
  - `scanner.go` — /proc enumeration into `ProcessInfo` structs
  - `proc_readers.go` — /proc metadata readers (owner, status, create time, ppid)
  - `service_name.go` — Service name extraction and sanitization (shared across handlers): `cleanName()`, `serviceNameFromWorkDir()`, `extractSystemdUnit()`, `extractServiceNameFromEnviron()`
  - `service.go` — Systemd unit helpers (cgroup parsing, ignored unit list)
  - `instrumentation.go` — `AgentType`, `ServiceSetting`, LD_PRELOAD detection
  - `report.go` — `ServiceSetting` struct, `GetAgentReportValue()` for backend reporting
  - `cache.go` — Process metadata cache (keyed by PID + create time, 20-min TTL)
  - `container.go` — Container detection via /proc cgroup (Docker, Podman, containerd, LXC, K8s)
  - `container_client.go` — `ContainerClient` interface, batch resolution, service name application
  - `container_client_docker.go` — Docker implementation via HTTP over `/var/run/docker.sock`
  - `container_client_podman.go` — Podman implementation via Docker-compat API
  - `tomcat.go` — Tomcat-specific webapp scanning
  - `ports.go` — Network listener discovery via `/proc/<pid>/fd` + `/proc/<pid>/net/*`
- **pkg/otelinject/** — OTel injection; primary integration point for mw-agent
  - `interfaces.go` — `Language` alias (= discovery.Language), `OtelInjector` interface
  - `strategy.go` — `InstrumentationStrategy` interface + `StrategyRegistry`
  - `systemd_strategy.go` — `SystemdDropinStrategy` (instruments via systemd drop-in files)
  - `obi_strategy.go` — `OBIStrategy` (instruments via OBI YAML selectors + obi-agent restart); `obiLanguageMap` translates internal language constants to OTel semconv names for OBI
  - `obiconfig.go` — OBI YAML config management with yaml.Node round-tripping
  - `obi_api.go` — High-level OBI API: `InstrumentOBI()`, `UninstrumentOBI()`, `InstrumentOBIBulk()`
  - `services_api.go` — Unified `DiscoverServices()` API with fingerprint grouping
  - `dropin.go` — Drop-in file creation/removal at `/etc/systemd/system/{unit}.service.d/`
  - `systemd_api.go` — High-level systemd API: `InstrumentUnit()`, `ListSystemdServices()`, `ReportStatus()`
  - `injector_java.go` — `JavaSystemdInjector` (OtelInjector for Java)
  - `injector_node.go` — `NodeSystemdInjector` (OtelInjector for Node)
  - `injector_python.go` — `PythonSystemdInjector` (OtelInjector for Python)
  - `validate.go` — Agent asset validation + libc flavor detection
- **pkg/systemd/** — Systemd service management (status, restart, drop-in cleanup)
- **pkg/agent/** — Java agent installation, validation, and permission management
- **pkg/config/** — Middleware.io environment variable configuration (~20 MW_* env vars)
- **pkg/naming/** — Service name generation with sanitization rules
- **pkg/state/** — JSON-based state persistence for tracking instrumentation
- **pkg/reporter/** — Backend API reporting client

### Key Patterns

**Discovery Pipeline:** Registry-driven with `LanguageHandler` interface. Each handler implements Detect → Enrich → PassesFilter → ToServiceSetting. Entry points:
- `FindAllProcesses(ctx)` — discovers all supported language processes
- `FindProcessesByLanguage(ctx, lang)` — discovers processes for a specific language

**Instrumentation Strategies:** Pluggable via `InstrumentationStrategy` interface. Strategies are tried in registration order; `CanHandle()` determines applicability:
1. `SystemdDropinStrategy` — tried first, requires `SystemdUnit != ""`
2. `OBIStrategy` — tried second, handles any process via eBPF (requires obi-agent installed)

**Process Fingerprint:** `Process.Fingerprint()` generates a stable identity hash (SHA256 of exe_path + language-specific args). Ports are deliberately excluded to keep fingerprints stable during app startup (port may not be bound yet). Used as the workload class identity — all replicas of the same app share a fingerprint. Populated in `ServiceSetting.Fingerprint` by all handlers.

**Container Detection:** Cgroup-based runtime detection (`docker/containerd`, `podman`, `kubernetes`, `lxc`). Container names resolved in batch via `ContainerClient` interface (HTTP over Unix socket, no Docker SDK dependency).

**Service Name Resolution:** Priority-based heuristic chain (first match wins, varies by language):

Java (java_handler.go `extractServiceName`):
1. Container name → 2. `OTEL_SERVICE_NAME`/`SERVICE_NAME` env → 3. Systemd unit name → 4. `-Dspring.application.name`/`-Dservice.name` → 5. JAR filename → 6. Main class → 7. JAR directory → 8. `"java-service"`

Node.js (node_handler.go `extractServiceName`):
1. Systemd unit name → 2. `--name=`/`--service=`/`SERVICE_NAME=` CLI flags → 3. package.json name → 4. Entry point filename (any first positional arg incl. extensionless) → 5. Working directory (last 2 meaningful segments via `serviceNameFromWorkDir`) → 6. `"node-service"`

Python (python_handler.go `extractServiceName`):
1. `OTEL_SERVICE_NAME`/`SERVICE_NAME`/`FLASK_APP` env → 2. Container name → 3. Virtualenv parent dir → 4. WSGI module / `.py` basename → 5. Script parent directory → 6. Working directory (last 2 meaningful segments via `serviceNameFromWorkDir`) → 7. `"python-service"`

**Cgroup Unit Filtering:** `parseCgroupUnitName()` skips `user@*` (user session manager) and `app-*` (transient desktop units per [systemd Desktop Environment spec](https://systemd.io/DESKTOP_ENVIRONMENTS/)). This prevents desktop terminal tab names from being used as service names and ensures terminal-launched processes get `serviceType = "system"` (not "systemd").

**Node.js Launcher Filtering:** `isNodeLauncher()` filters npm/npx/yarn/pnpm/corepack processes in `Enrich()` by checking `cmdArgs[0]`. These are package manager launchers whose exe resolves to `node` via symlink — they shadow the real app process in the settings map.

**Service Lookup:** `findService()` in obi_api.go accepts either service name or fingerprint. Tries name match first; if ambiguous (same name, different fingerprints), requires fingerprint disambiguation.

**Systemd Integration:** Drop-in files created at `/etc/systemd/system/{service}.service.d/middleware-otel.conf` using LD_PRELOAD for Java/Node or LD_PRELOAD + PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX for Python.

**OBI Integration:** Selectors added to `/etc/obi-agent/config.yaml` under `discovery.instrument[]`. YAML round-tripped via `yaml.Node` to preserve comments and unrelated sections. OBI agent restarted via systemctl after config changes. OBI uses OTel semconv language names (`nodejs`, not `node`); the `obiLanguageMap` in `obi_strategy.go` handles the translation from internal language constants.

**State Persistence:** JSON files at `/etc/middleware/state/` track instrumented services to enable clean uninstrumentation.

### Key Data Structures

- `Process` (pkg/discovery/process.go) — Unified process representation with OTel fields + `Details map[string]any` + `Fingerprint()`
- `LanguageHandler` (pkg/discovery/registry.go) — Interface for language-specific pipeline phases
- `ServiceSetting` (pkg/discovery/report.go) — Backend reporting payload per service (includes Fingerprint, InstrumentationType)
- `ServiceEntry` (pkg/otelinject/services_api.go) — Fingerprint-grouped workload with instance list
- `InstrumentationStrategy` (pkg/otelinject/strategy.go) — Interface for pluggable instrumentation methods
- `OBISelector` (pkg/otelinject/obiconfig.go) — OBI YAML selector (name, open_ports, exe_path, cmd_args, languages, containers_only)
- `ContainerClient` (pkg/discovery/container_client.go) — Interface for container runtime APIs (Docker, Podman)
- `ProcessConfiguration` (pkg/config/config.go) — Middleware.io env var configuration
- `DiscoveryOptions` (pkg/discovery/discovery.go) — Controls concurrency, timeout, filtering

### Default Paths

| Component | Path |
|-----------|------|
| Java Agent | `/opt/middleware/agents/middleware-javaagent-1.8.1.jar` |
| State Files | `/etc/middleware/state/` |
| Config Files | `/etc/middleware/services/` |
| Systemd Drop-ins | `/etc/systemd/system/{unit}.service.d/middleware-otel.conf` |
| OBI Config | `/etc/obi-agent/config.yaml` |
| OBI Binary | `/usr/local/bin/obi` |

## Adding New Features

- **New language:** Implement `LanguageHandler` in `pkg/discovery/{lang}_handler.go`, register in `NewHandlerRegistry()`. No changes to core pipeline.
- **New instrumentation method:** Implement `InstrumentationStrategy` in `pkg/otelinject/{method}_strategy.go`, register in `NewStrategyRegistry()`.
- **New container runtime:** Implement `ContainerClient` in `pkg/discovery/container_client_{runtime}.go`, add to `initContainerClients()`.
- **New config field:** Extend `ProcessConfiguration` in `pkg/config/config.go`

## Ideas / TODO

- **Drop `--language` from instrument command:** `InstrumentOBI` currently requires a language param to filter discovery. The user shouldn't need to specify it — discover across all languages, find the service by name/fingerprint, and read the language from the match. Tradeoff: ~3x discovery cost (all languages vs one), but acceptable for a one-shot CLI command. `resolveFingerprint` and `ListOBIServices` already do language-agnostic discovery.
- **Per-port instrumentation:** Currently instrumenting by name/fingerprint aggregates ports from all instances sharing a fingerprint. No way to instrument only one instance (e.g., port 3000) out of a group. Would need an optional `--port` filter that skips aggregation and builds the OBI selector with only the specified port.
- **User-defined service name and port overrides:** Allow users to rename a discovered service and/or manually set its port number via CLI or UI. Overrides would be stored in the backend (keyed by fingerprint) and applied during discovery merge — taking precedence over heuristic-derived values. The override port would flow into OBI selectors and systemd drop-in config. Useful when auto-detection produces a generic name or when ports are invisible (e.g., PM2 cluster workers where the daemon holds the listen socket).

## CodeGraph

CodeGraph builds a semantic knowledge graph of codebases for faster, smarter code exploration.

### If `.codegraph/` exists in the project

**NEVER call `codegraph_explore` or `codegraph_context` directly in the main session.** These tools return large amounts of source code that fills up main session context. Instead, ALWAYS spawn an Explore agent for any exploration question (e.g., "how does X work?", "explain the Y system", "where is Z implemented?").

**When spawning Explore agents**, include this instruction in the prompt:

> This project has CodeGraph initialized (.codegraph/ exists). Use `codegraph_explore` as your PRIMARY tool — it returns full source code sections from all relevant files in one call.
>
> **Rules:**
> 1. Follow the explore call budget in the `codegraph_explore` tool description — it scales automatically based on project size.
> 2. Do NOT re-read files that codegraph_explore already returned source code for. The source sections are complete and authoritative.
> 3. Only fall back to grep/glob/read for files listed under "Additional relevant files" if you need more detail, or if codegraph returned no results.

**The main session may only use these lightweight tools directly** (for targeted lookups before making edits, not for exploration):

| Tool | Use For |
|------|---------|
| `codegraph_search` | Find symbols by name |
| `codegraph_callers` / `codegraph_callees` | Trace call flow |
| `codegraph_impact` | Check what's affected before editing |
| `codegraph_node` | Get a single symbol's details |

### If `.codegraph/` does NOT exist

At the start of a session, ask the user if they'd like to initialize CodeGraph:

"I notice this project doesn't have CodeGraph initialized. Would you like me to run `codegraph init -i` to build a code knowledge graph?"
