# MW Injector

Go library for process discovery and auto-instrumentation on Linux hosts. Discovers Java, Node.js, and Python applications across host processes, Docker/Podman containers, and systemd services, then instruments them via systemd drop-ins or OBI (OpenTelemetry eBPF Instrumentation).

Used as a library by [mw-agent](https://github.com/middleware-labs/mw-agent) — not a standalone CLI.

## Usage via mw-agent

```bash
# List all discovered services (grouped by fingerprint)
sudo mw-agent services list

# Show individual instances
sudo mw-agent services list --all

# Filter by language (both "node" and "nodejs" are accepted)
sudo mw-agent services list --language java

# Instrument via OBI (by service name or fingerprint)
sudo mw-agent instrument --type obi --language java book-service-java-docker
sudo mw-agent instrument --type obi --language java dbeb5aa2b3f80d19

# Instrument via systemd drop-in
sudo mw-agent instrument --type systemd --language java my-service

# Uninstrument
sudo mw-agent uninstrument --type obi book-service-java-docker
sudo mw-agent uninstrument --type systemd my-service
```

### Example output

```
FINGERPRINT        SERVICE NAME                  LANGUAGE  TYPE      PORTS  INSTANCES  INSTRUMENTED
e6f38b1db677d920   book-service-java             java      systemd   8086   1          -
9fff4fcb924d5413   book-service-java-docker      java      docker    8086   1          -
8d3392312658de2e   browse-bay-backend            node      system    3000   1          -
8544b3b66cebbf8d   unattended-upgrade-shutdown   python    systemd   -      1          -
```

## Architecture

### Discovery Pipeline

Registry-driven with `LanguageHandler` interface. Each handler implements Detect, Enrich, PassesFilter, and ToServiceSetting:

```
/proc scan → classify by language → enrich (args, env, listeners) → batch container resolution → filter → ServiceSetting
```

### Instrumentation Strategies

Pluggable via `InstrumentationStrategy` interface, tried in order:

1. **SystemdDropinStrategy** — creates `/etc/systemd/system/{unit}.service.d/middleware-otel.conf` with LD_PRELOAD. Requires the process to be managed by a systemd unit.
2. **OBIStrategy** — adds YAML selectors to `/etc/obi-agent/config.yaml` and restarts the obi-agent service. Handles any process including non-systemd ones via eBPF. Language names are translated to OTel semantic convention values (e.g., `node` becomes `nodejs`) before writing selectors. A warning is logged when a service has no listening ports detected.

### Process Fingerprint

Stable workload identity hash (SHA256 of exe_path + language-specific args). Ports are deliberately excluded — they can be unavailable during startup and would cause fingerprint instability. All replicas of the same app share a fingerprint. Used for grouping in `services list` and as an alternative identifier for instrument/uninstrument commands.

### Service Name Resolution

Each language handler implements a priority-based heuristic chain (first match wins):

**Java:** Container name → `OTEL_SERVICE_NAME`/`SERVICE_NAME` env → Systemd unit name → `-Dspring.application.name` / `-Dservice.name` → JAR filename → Main class → JAR directory

**Node.js:** Systemd unit name → `--name=`/`--service=`/`SERVICE_NAME=` CLI flags → package.json name → Entry point filename (any first positional arg, including extensionless scripts) → Working directory (last 2 meaningful segments)

**Python:** `OTEL_SERVICE_NAME`/`SERVICE_NAME`/`FLASK_APP` env → Container name → Virtualenv parent directory → WSGI module name / `.py` script basename → Script parent directory → Working directory (last 2 meaningful segments)

**Filtering rules:**
- Systemd unit names from user-session desktop units (`app-*` prefix per systemd Desktop Environment spec) are skipped — the name falls through to the next heuristic
- Node.js package manager launchers (npm, npx, yarn, pnpm, corepack) are filtered from discovery entirely — only the actual app process is kept
- Generic names (index, app, main, server, java, etc.) are rejected and fall through to the next heuristic

### Container Detection

Cgroup-based runtime detection distinguishes Docker, Podman, containerd, and LXC. Container names resolved in batch via HTTP over Unix socket (no Docker SDK dependency). Supports Docker (`/var/run/docker.sock`) and Podman (`/run/podman/podman.sock`).

## Package Structure

| Package | Purpose |
|---------|---------|
| `pkg/discovery/` | Process discovery engine, language handlers, container detection |
| `pkg/otelinject/` | Instrumentation strategies, OBI config management, service listing API |
| `pkg/systemd/` | Systemd service management |
| `pkg/agent/` | Java agent installation and validation |
| `pkg/config/` | Middleware.io environment variable configuration |
| `pkg/state/` | JSON-based state persistence |
| `pkg/reporter/` | Backend API reporting client |

## Build

```bash
go build ./...
go test ./...
```

## Requirements

- Linux (systemd-based distributions)
- Go 1.24+
- Root privileges for instrumentation
- Docker/Podman (optional, for container discovery)
- OBI agent (optional, for eBPF instrumentation)
