# Workload Identity Research: What Makes a Linux Process Fingerprint Stable?

## The Core Question

What makes two process instances "the same workload" across restarts, redeployments, and upgrades? What combination of observable properties from `/proc` should we hash to get a fingerprint that represents **workload class identity**, not **instance identity**?

---

## 1. Formal Definitions

The IETF WIMSE architecture (draft-ietf-wimse-arch-07) makes the distinction explicit:

- **Workload**: "An independently addressable software entity executing for a specific purpose."
- **Workload Instance**: "A single running instantiation... which may exist for a fraction of a second or for extended periods."

This is the foundational split: a workload is the *class*, an instance is one execution of it. Our fingerprint must identify the class.

---

## 2. OpenTelemetry Resource Semantic Conventions

OTel defines identity across two dimensions:

### Service-level (logical workload identity)

| Attribute | Stability | Role |
|---|---|---|
| `service.name` | Stable — human-assigned | Primary identity anchor. Not tied to any process or PID. Represents *what the service is*. |
| `service.namespace` | Stable | Optional logical grouping (team, org unit). |
| `service.version` | Mutable across upgrades | Part of workload identity but does not *define* the workload class. |
| `service.instance.id` | Ephemeral | OTel spec: "unique across all instances of the same service." Explicitly ephemeral across restarts. Maps to instance identity, not workload identity. |

### Process-level (execution context)

| Attribute | Stability | Role |
|---|---|---|
| `process.executable.path` | Stable within a deployment, changes on version bump | The binary path. |
| `process.runtime.name` | Stable | e.g., `cpython`, `nodejs`, `openjdk`. Survives version upgrades within a runtime family. |
| `process.runtime.version` | Changes on runtime upgrades | OTel classifies this as part of identity but acknowledges it will differ across upgrade windows. |
| `process.pid` | Ephemeral | OTel recommends it for correlation within a session only. |
| `process.command_line` | Noisy | OTel guidance prefers `process.command_args` because the full command line carries ephemeral elements (temp file paths, port numbers, etc.). |

**Key OTel principle:** `service.name` + `service.namespace` = workload class identity. Everything else either refines it or tracks instance state. The OTel Operator on Kubernetes reinforces this: it injects `k8s.deployment.name` and `k8s.daemonset.name` as the stable workload identifiers, while `k8s.pod.uid` and `k8s.pod.name` are instance-scoped and ephemeral.

Sources:
- https://opentelemetry.io/docs/specs/semconv/resource/
- https://opentelemetry.io/docs/specs/semconv/resource/process/

---

## 3. Kubernetes Workload Identity

Kubernetes resolves this through the controller/selector model:

- **Stable (workload class):** `k8s.deployment.name`, `k8s.daemonset.name`, `k8s.statefulset.name` — these survive pod restarts, rolling updates, scaling, and host migration. The Deployment UID persists across rollouts; only the pod UIDs cycle.
- **Label selectors** encode workload class membership. The canonical labels `app.kubernetes.io/name` and `app.kubernetes.io/component` are the intended stable cross-version workload identifiers.
- **Ephemeral:** `k8s.pod.uid`, `k8s.pod.name`, node assignment, container image SHA. A pod can be rescheduled to a different node with a new image and a new UID — and it's still "the same workload."
- **StatefulSets** are the one place where instance identity is deliberately stabilized (`<name>-<ordinal>`), but even here the *workload* is the StatefulSet name, not any individual pod.

For Linux host processes, there is no K8s controller layer — the equivalent is the **systemd unit name** or the **process's deployment path**.

Sources:
- https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/
- https://kubernetes.io/docs/concepts/workloads/controllers/deployment/

---

## 4. systemd Service Identity

systemd's model is the clearest: **the unit name is the stable identity**. The cgroup path `/sys/fs/cgroup/system.slice/myapp.service` encodes the unit name, not the PID. When a service restarts, the cgroup path survives; only the leaf PID changes.

- Unit name = workload class identity
- PID = instance identity

For processes *not* managed by systemd, the cgroup path degrades to `/sys/fs/cgroup/user.slice/...` or `session-N.scope` — both ephemeral, neither stable. Cgroup-based unit extraction is only useful when a unit name can be parsed from the path.

Sources:
- https://systemd.io/CGROUP_DELEGATION/

---

## 5. How APM/Observability Tools Fingerprint Processes

### Dynatrace (most thoroughly documented)

Dynatrace groups processes into "process groups" — their equivalent of workload class — using:

1. **Executable path** (directory containing the binary, not just the binary name)
2. **Technology-specific deployment directories** (e.g., `CATALINA_HOME` for Tomcat, `JBOSS_SERVER_NAME` for JBoss)
3. **Selected command-line arguments** (declaratively configured via rules, not the full command line)
4. **Environment variables** for cluster/node disambiguation (e.g., `DT_CLUSTER_ID`)

**Dynatrace explicitly *excludes* version strings from the grouping key**, because version bumps should not create a new process group. The version appears as an attribute on the process group, not in its identity hash.

Sources:
- https://docs.dynatrace.com/docs/observe/infrastructure-monitoring/process-groups/configuration/declarative-process-grouping
- https://docs.dynatrace.com/docs/observe/infrastructure-monitoring/process-groups/configuration/pg-detection

### Elastic APM

Focuses on `service.name` + `service.environment` + destination address as the fingerprint for service map edges — not process-level hashing. Service identity is configured, not inferred from the process.

Sources:
- https://github.com/elastic/apm-server/issues/3335

### New Relic

Follows the same pattern: `service.name` from `NEW_RELIC_APP_NAME` or equivalent environment variable is the stable identity; everything else is metadata.

---

## 6. SPIFFE/SPIRE Attestation Selectors

SPIFFE takes the most formal approach: a workload is identified by a set of *attestation selectors* — observable properties evaluated at runtime. Multiple instances sharing the same selector set receive the same SPIFFE ID (e.g., `spiffe://prod.acme.com/billing/api`).

Selector properties eligible for workload-class attestation on Linux:

| Selector | Stability |
|---|---|
| `unix:uid` (process owner UID) | Stable for a given service account |
| `unix:path` (executable path) | Stable within a deployment |
| `k8s:sa` (service account name) | Stable across pod churn |
| `k8s:ns` (namespace) | Stable across pod churn |

**Explicitly excluded from workload-class selectors:** PID, pod name, pod UID, image tag/SHA.

This is the clearest industry definition of "same workload = same set of invariant observable properties."

Sources:
- https://spiffe.io/docs/latest/spiffe-about/spiffe-concepts/

---

## 7. The Invariance Taxonomy

Drawing across OTel, K8s, systemd, Dynatrace, and SPIFFE:

### Properties that ALWAYS persist (the workload's "essence" — hash these)

| Property | Source | Notes |
|---|---|---|
| **Language class** | OTel `process.runtime.name` | `java`, `nodejs`, `python` — survives any version upgrade within the runtime family |
| **Logical entry point** | Dynatrace, OTel `process.command` | For Java: main class or JAR base name stripped of version. For Node: entry point filename. For Python: module path or script name. |
| **Systemd unit name** (when present) | systemd, OTel `service.name` | The single highest-quality invariant when available |
| **Container name** (when containerized) | K8s `k8s.deployment.name`, Docker name | Human-assigned and survive image rebuilds |
| **Explicit service name env vars** | OTel `service.name` | `OTEL_SERVICE_NAME`, `SERVICE_NAME` — human-assigned override |

### Properties that SOMETIMES persist (deployment-dependent — use as secondary signals)

| Property | Survives? | Notes |
|---|---|---|
| Full executable path including binary | Survives restarts, NOT version bumps if path includes version | `app-1.0.0.jar` → `app-1.0.1.jar` breaks it |
| Executable deployment directory | Usually stable | `/opt/myapp/bin/` survives binary upgrades |
| Runtime version (`java.version`, `python.version`) | Survives restarts, NOT upgrades | Java 17→21 changes this |
| Working directory | Usually stable, breaks on path changes | Good secondary signal, not primary |
| Package name (`package.json#name`) | Survives restarts and version bumps | Highly stable for Node.js if present |
| Listening port | Often stable in practice, not guaranteed | Changes during rolling deploys, not during restarts |

### Properties that NEVER persist (always ephemeral — never hash these)

| Property | Why ephemeral |
|---|---|
| PID | Changes on every restart; can be reused |
| Pod UID / container ID | Regenerated on every pod/container creation |
| Full command line with all args | May include temp paths, session tokens, or port numbers |
| Memory/CPU metrics | Runtime state |
| Process create time | Instance-scoped timestamp |
| IP address | Changes on host migration, pod rescheduling |
| Runtime version string (as fingerprint input) | Breaks fingerprint on Java 17→21, Python 3.10→3.12 |
| JAR path with version in filename | `app-1.0.0.jar` ≠ `app-1.0.1.jar` |

---

## 8. What This Means for Our Fingerprint

### Current implementation (`process.go:Fingerprint()`)

Hashes: `ExecutablePath` + language-specific fields (`DetailJarFile`, `DetailMainClass`, `DetailEntryPoint`, `DetailModulePath`)

### Identified gaps

#### Gap 1: ExecutablePath includes full path with runtime version directories

`/usr/lib/jvm/java-17-openjdk/bin/java` vs `/usr/lib/jvm/java-21-openjdk/bin/java` — upgrading the runtime changes the fingerprint. Same for `/usr/bin/python3.10` vs `/usr/bin/python3.12`.

**Industry precedent:** OTel uses `process.runtime.name` (just `java`, `nodejs`, `python`) as the stable identifier. Dynatrace uses the deployment directory, not the runtime binary path. SPIFFE's `unix:path` refers to the application binary, not the interpreter.

**Fix:** Use `filepath.Base(ExecutablePath)` (just `java`, `node`, `python3`) instead of the full path. The language-specific fields already carry the workload identity — the exe path just needs to distinguish the runtime family.

#### Gap 2: JAR filename includes version

`DetailJarFile` stores the raw basename like `app-1.0.0.jar`. When you redeploy `app-1.0.1.jar`, the fingerprint changes. The service name logic (`extractNameFromJar`) already strips versions — but `Fingerprint()` uses the raw value.

**Industry precedent:** Dynatrace explicitly excludes version strings from process group identity. OTel treats `service.version` as mutable metadata, not identity.

**Fix:** Strip version suffixes and `.jar` extension from the jar file before hashing — reuse the same patterns from `extractNameFromJar`. So `app-1.0.0.jar` and `app-1.0.1-SNAPSHOT.jar` both contribute `app` to the hash.

#### Gap 3: Python doesn't include DetailEntryPoint in fingerprint

`python app.py` sets `DetailEntryPoint` but `Fingerprint()` only checks `DetailModulePath` for Python. A plain `python app.py` process gets fingerprinted on just the executable path — any other Python process on the same host with no `-m` flag would collide.

**Industry precedent:** All systems above include the logical entry point as a core identity component regardless of invocation style.

**Fix:** Include `DetailEntryPoint` for Python in the fingerprint, same as Node does.

#### Gap 4: Process managers rewrite `/proc/<pid>/cmdline` (argv overwriting)

PM2, forever, and similar Node.js process managers overwrite the process argv buffer at runtime. Instead of null-separated args (`node\0/path/to/index.js\0`), the cmdline becomes a single space-joined string (`node /path/to/index.js\0\0\0...`) padded with trailing null bytes. This causes `readProcDetails` to produce a single-element `CmdArgs` slice, which means:

- `extractNodeInfo` finds no positional args (only `cmdArgs[0]` exists, and `i==0` is skipped) → empty entry point
- `Fingerprint()` hashes only `ExecutablePath` → all Node apps on the same runtime collapse to the same hash
- `detectProcessManager` can't parse flags from the cmdline

**Observed:** PM2 God Daemon (`PM2 v6.0.14: God Daemon (/home/user/.pm2)`) and the actual app process (`node /path/to/index.js`) both resolve exe to `/home/user/.nvm/.../bin/node`, both have cwd `/path/to/app/`, both get empty entry point, both get fingerprint `61c2c72c045b9bb9`, both get service name `browse-bay-backend` from `serviceNameFromWorkDir`. Last writer wins in the settings map → service ID, type, and port flicker between runs depending on `/proc` scan order.

**Fix applied (scanner.go):** `parseCmdline()` detects single-arg cmdlines where the first word matches the exe name and re-splits on whitespace. This recovers the original arguments for the app process.

**Fix applied (node_handler.go):** Added `"pm2"` to `nodeLaunchers` map so the PM2 God Daemon is filtered out by `isNodeLauncher()`. The daemon rewrites its argv[0] to `PM2 v<version>: God Daemon (...)` — first word is "pm2".

**Residual risk:** Any process manager that rewrites argv in a way that does NOT start with the exe name will bypass `parseCmdline`'s re-split heuristic. The fingerprint would fall back to just the exe path, collapsing with other processes on the same runtime. This is a general fragility of relying on cmdline-derived fields for identity.

#### Gap 5: npm/yarn/pnpm launched via `sh -c` produce space-joined cmdlines

When a process is started via `sh -c "npm start"`, the shell invokes node (via symlink) with a single space-joined cmdline arg: `npm start\0`. The `isNodeLauncher` check was doing exact map lookup on `cmdArgs[0]` for `"npm"`, but received `"npm start"` (with space) — so the launcher was not filtered.

**Observed:** `npm start` process (PID 109063) and the actual `node index.js` worker (PID 109075) both ran from the same cwd, both got service name `browse-bay-backend`, but had different fingerprints. Since both mapped to Key `host-node-browse-bay-backend`, last writer wins in the settings map → fingerprint, type, and port flickered between runs.

**Fix applied (node_handler.go):** `isNodeLauncher()` now extracts the first word from `cmdArgs[0]` before the map lookup, handling both null-separated (`"npm"`) and space-joined (`"npm start"`) cmdline formats.

#### Gap 6: `settings[ss.Key]` map allows silent overwrites

The `AutoInstrumentationSettings` map in `report.go` is `map[string]ServiceSetting` keyed by `ss.Key` (e.g., `host-node-browse-bay-backend`). When two processes produce the same Key but different fingerprints, the second silently overwrites the first. This means:

- One process disappears entirely from the report
- The "winner" depends on `/proc` scan order, which varies between runs
- All downstream consumers (CLI, frontend, backend) see flickering service attributes

This is the structural root cause behind Gaps 4 and 5. Even after filtering known launchers/daemons, any future edge case where two processes share a Key will cause the same bug. The fix is to change the map key from `ss.Key` to `ss.Fingerprint` and change the value type to `[]ServiceSetting` to support multiple instances per workload.

#### Gap 7: No fallback chain in `Fingerprint()` — empty language-specific fields produce collisions

`Fingerprint()` hashes `ExecutablePath` plus language-specific fields, but if all language-specific fields are empty (no entry point, no jar file, no module path), the fingerprint degrades to just the exe path. Every process on the same runtime (e.g., all Node apps using `/usr/bin/node`) gets the same fingerprint.

**Industry precedent:** Dynatrace uses a priority chain for process group identity — technology-specific deployment directory → selected command-line arguments → environment variables → executable path. Each level is a fallback, not a standalone hash input.

**Fix:** The fingerprint should use an additive fallback chain. Core fields that are always available (language, working directory) form the base. Language-specific fields (entry point, jar name, module path, package.json name, systemd unit, container name) are added when present. The combination should be unique per workload class even when cmdline-derived fields are missing.

Proposed priority for fingerprint inputs (hash all that are available):

| Priority | Input | Availability | Notes |
|----------|-------|-------------|-------|
| 1 | Language (`java`, `node`, `python`) | Always | Replaces full exe path — survives runtime upgrades |
| 2 | Systemd unit name | When managed by systemd | Strongest invariant on Linux per systemd spec |
| 3 | Container name | When containerized | Human-assigned, survives image rebuilds |
| 4 | `OTEL_SERVICE_NAME` / `SERVICE_NAME` env | When set | Explicit human-assigned identity |
| 5 | Language-specific entry point | Usually available | JAR name (sans version), Node entry point / `package.json:name`, Python module/script |
| 6 | Working directory | Almost always available | Stable across restarts; breaks on deployment path changes |
| 7 | `ExecutableName` (basename only) | Always | Last resort — just `java`, `node`, `python3` |

#### Gap 8: Node.js `package.json:name` is not read

The Node handler detects `package.json` existence (line 239) but sets `DetailPackageName = "unknown"` instead of actually parsing the file. `package.json:name` is the canonical project identity chosen by the developer — it survives restarts, argv rewrites, PID rotation, and even entry point changes. It's the Node.js equivalent of a systemd unit name.

**Fix:** Read and parse `package.json` to extract the `name` field. Use it as:
1. A fingerprint input (high priority, after systemd unit / container name)
2. A service name source (already in the `extractServiceName` priority chain at position 3, but currently dead code because the name is never populated)

#### Gap 9: Python fingerprint collision — completely unrelated workloads get same identity

Two completely different Python processes on the same host produce identical fingerprint `8544b3b66cebbf8d`:

| | fastapi-bookstore-api (PID 452522) | unattended-upgrade-shutdown (PID 1922) |
|---|---|---|
| exe (`/proc/<pid>/exe`) | `/usr/bin/python3.13` | `/usr/bin/python3.13` (symlink from `/usr/bin/python3`) |
| cmdline | `/home/hardik/.../python3 ... uvicorn main:app --host 0.0.0.0 --port 8000 --workers 4` | `/usr/bin/python3 /usr/share/unattended-upgrades/unattended-upgrade-shutdown --wait-for-signal` |
| cwd | `/home/hardik/systemd-services/python/fastapi-bookstore-api` | (none — readlink failed, root-owned process) |
| `DetailModulePath` | empty — uvicorn invoked directly, not via `-m` | empty — no `-m` flag |
| `DetailEntryPoint` | empty — `uvicorn` has no `.py` suffix, `main:app` has no `.py` suffix | empty — `unattended-upgrade-shutdown` has no `.py` suffix |
| **Fingerprint** | `SHA256("/usr/bin/python3.13")` = `8544b3b66cebbf8d` | `SHA256("/usr/bin/python3.13")` = `8544b3b66cebbf8d` |

**Why it happens — three failures compound:**

1. **`Fingerprint()` only checks `DetailModulePath` for Python** (Gap 3). It doesn't check `DetailEntryPoint`, working directory, or any other signal. When `DetailModulePath` is empty, the fingerprint is just the exe path.

2. **`extractPythonInfo()` only sets `DetailEntryPoint` for args ending in `.py`** (line 238: `strings.HasSuffix(arg, ".py")`). But many Python tools — uvicorn, gunicorn, celery, flask, and system scripts like `unattended-upgrade-shutdown` — don't have `.py` extensions. The WSGI module notation `main:app` also doesn't match. So `DetailEntryPoint` stays empty for a large class of real-world Python processes.

3. **`extractPythonInfo()` only sets `DetailModulePath` for `-m` invocations** (line 250: `cmdArgs[i-1] == "-m"`). Direct invocation of tools (`uvicorn main:app`) and scripts (`/usr/share/.../unattended-upgrade-shutdown`) bypass this entirely.

**Observed impact:** The `services list` command shows service name flickering between `fastapi-bookstore-api` and `unattended-upgrade-shutdown` across consecutive runs. The service name resolution chain correctly differentiates them (virtualenv path → `fastapi-bookstore-api`, script path → `unattended-upgrade-shutdown`), but since both produce the same fingerprint and the same Key format (`host-python-{name}`... different names but same fingerprint), they collide in the settings map. Which one survives depends on `/proc` scan order.

**This is Gap 7 in its purest form:** the fingerprint has exactly one language-specific input for Python (`DetailModulePath`), and when it's empty — which it is for the majority of real-world Python processes — every Python process on the same runtime collapses to a single identity. A user-facing web API and a system maintenance daemon become indistinguishable.

**What the new fingerprint design (Section 9) would produce:**

- `fastapi-bookstore-api`: `SHA256("python" + "/home/hardik/systemd-services/python/fastapi-bookstore-api")` — working directory provides discrimination
- `unattended-upgrade-shutdown`: `SHA256("python" + "/usr/share/unattended-upgrades")` — completely different hash

Even without fixing `extractPythonInfo`'s entry point parsing, the additive fingerprint with working directory would have prevented this collision.

---

## 9. Finalized Fingerprint Design

### Design principles

1. **Additive, not selective** — every available signal goes into the hash. An empty field is simply omitted (contributes no segment). No single missing field can cause a collision.
2. **Raw observables only, not derived values** — the fingerprint hashes properties read from `/proc`, cgroups, env vars, and container APIs. The resolved `service_name` is a derived heuristic and is **excluded** — it's a display label, not identity. Exception: `OTEL_SERVICE_NAME`/`SERVICE_NAME` from env vars are explicit human declarations and ARE included.
3. **Version-agnostic** — runtime version, jar version suffixes, and full exe paths that embed versions are excluded or normalized. A Java 17→21 upgrade or Python 3.12→3.13 upgrade must not change the fingerprint.
4. **Ports excluded** — they bind late during startup and may change across deploys.
5. **PID/create time excluded** — ephemeral instance identity.
6. **Machine-local scope** — fingerprints identify workload classes on a single host. Same app deployed to different paths on different hosts may get different fingerprints. This is correct — mw-injector is a host-level tool.

### Hash inputs (all that are available are included)

```
Fingerprint = SHA256(
    language                    // "java" | "node" | "python" — always present
    systemd_unit                // from cgroup parsing, if present
    container_name              // from container runtime API, if containerized
    explicit_service_name       // OTEL_SERVICE_NAME or SERVICE_NAME from /proc/<pid>/environ
    language_entry_point        // Java: jar basename (version-stripped) + main class
                                // Node: package.json:name or entry point filename
                                // Python: -m module path OR script basename OR first positional arg
    working_directory           // /proc/<pid>/cwd — almost always available
)
```

### Input priority and availability

| # | Input | Availability | Stability | Notes |
|---|-------|-------------|-----------|-------|
| 1 | `language` | Always | Permanent | Replaces full exe path — `"java"`, `"node"`, `"python"` |
| 2 | `systemd_unit` | When managed by systemd | Very high | Strongest invariant on Linux per systemd spec |
| 3 | `container_name` | When containerized | High | Human-assigned, survives image rebuilds |
| 4 | `OTEL_SERVICE_NAME` / `SERVICE_NAME` env | When explicitly set | Very high | Human-declared identity — the ONLY derived-looking value we include, because it's explicit not heuristic |
| 5 | Language-specific entry point | Usually available | High | JAR basename sans version + main class; Node `package.json:name` or entry point; Python module/script |
| 6 | `working_directory` | Almost always | Medium-high | Stable across restarts; breaks on deployment path changes. Safety net that prevents exe-only collisions |

### What's removed vs current implementation

- **Full `ExecutablePath`** → replaced by `language` string (version-agnostic)
- **Version-stamped jar filenames** → strip version before hashing (reuse `extractNameFromJar` patterns)

### What's added vs current implementation

- **`language`** as namespace separator (prevents cross-language collisions)
- **`systemd_unit`** — strongest host-level invariant
- **`container_name`** — survives image rebuilds
- **`OTEL_SERVICE_NAME`/`SERVICE_NAME`** from `/proc/<pid>/environ`
- **`working_directory`** — safety net for the Python/Node exe-only collision case
- **Python `DetailEntryPoint`** (currently only `DetailModulePath` is checked)
- **Node `package.json:name`** (currently detected but set to `"unknown"`)

### What's explicitly excluded

| Excluded | Reason |
|----------|--------|
| Resolved `service_name` | Derived heuristic, not raw observable. Creates coupling — if name resolution logic changes, fingerprints change. The raw inputs that produce the name are already in the hash. |
| `ExecutablePath` (full) | Embeds runtime version (`/usr/bin/python3.13`, `/usr/lib/jvm/java-17/bin/java`). Breaks on upgrade. |
| `ExecutableName` (basename) | Redundant with `language` — both carry the same information but `language` is normalized. |
| Runtime version | Breaks on upgrade. Not part of workload class identity per OTel and Dynatrace. |
| Listening ports | Bind late, may change across deploys. |
| PID / create time | Ephemeral instance identity. |
| Full command line | Contains temp paths, session tokens, port numbers. |
| JAR version suffix | `app-1.0.0.jar` and `app-1.0.1.jar` are the same workload. |

### Collision analysis for known cases

| Case | Current fingerprint | New fingerprint | Collision? |
|------|-------------------|-----------------|------------|
| PM2 daemon + Node app (same cwd) | Both: `SHA256(/usr/bin/node)` — **COLLISION** | Daemon filtered by `isNodeLauncher`. App: `SHA256(node + entry_point + cwd)` | No |
| Two Python apps, no `-m` (uvicorn direct + unattended-upgrades) | Both: `SHA256(/usr/bin/python3.13)` — **COLLISION** | `SHA256(python + /home/.../fastapi-bookstore-api)` vs `SHA256(python + /usr/share/unattended-upgrades)` | No |
| Same app, Python 3.12→3.13 upgrade | Different (exe path changed) | Same — `language` is just `"python"`, cwd unchanged | Correct |
| Same Java app, jar 1.0→1.1 | Different (jar filename changed) | Same — jar basename version-stripped | Correct |
| Two Node apps in same directory, different entry points | Same if entry points were empty | Different — entry points included | Correct |
| Same app, two replicas (different PIDs, same everything else) | Same | Same — all inputs identical | Correct (workload class) |

### Prerequisites before implementation

1. **Node `package.json` parsing** (Gap 8) — must actually read the `name` field, not hardcode `"unknown"`
2. **Python entry point extraction** — must handle non-`.py` scripts and direct tool invocations (`uvicorn main:app`)
3. **Java jar version stripping** — reuse `extractNameFromJar` normalization in fingerprint
4. **Systemd unit name** — already available via `extractSystemdUnit()`, just needs to flow into `Fingerprint()`
5. **Env var reading** — `OTEL_SERVICE_NAME`/`SERVICE_NAME` already read by `extractServiceNameFromEnviron()`, needs to be stored as a Process detail so `Fingerprint()` can access it

### Migration cost

These changes will invalidate existing fingerprints for all processes on the next discovery scan. Any downstream system keyed on fingerprints (OBI selectors, state files, backend tracking) will see them as new workloads. This is a one-time cost.

---

## 10. Implementation Roadmap

Two phases — fingerprint stabilization first, then map-key migration. Phase 1 is a prerequisite for Phase 2.

### Phase 1: Stable Fingerprint — COMPLETED

**Status:** Implemented and committed (`433c9b3` on `hc-mw/port-detection`).

Goal: Make `Fingerprint()` produce unique, version-agnostic, stable hashes for all workload classes.

#### What was implemented

**1. New Process detail keys and shared enrichment (`service_name.go`, `process.go`)**

- Added `DetailSystemdUnit` and `DetailExplicitServiceName` constants
- Created `enrichCommonDetails(proc)` — shared function called by all 3 handlers during `Enrich()`, populating systemd unit (from cgroup), explicit service name (`OTEL_SERVICE_NAME`/`SERVICE_NAME` from `/proc/<pid>/environ`), and working directory
- Created `extractExplicitServiceName(pid)` — reads only `OTEL_SERVICE_NAME` and `SERVICE_NAME` (excludes `FLASK_APP` which is framework config, not explicit identity)
- `enrichCommonDetails` is called after each handler's `extractInfo()` and before `extractServiceName()`, ensuring all fingerprint inputs are populated before `ToServiceSetting()` calls `Fingerprint()`

**2. Python `extractPythonInfo` rewrite (`python_handler.go`)**

- Accepts first positional arg as entry point regardless of `.py` extension
- Handles `-m module` invocations (sets both `DetailModulePath` and `DetailEntryPoint`)
- Skips known Python tool names (uvicorn, gunicorn, celery, etc.) — continues to next positional arg which is the real entry point (e.g., `uvicorn main:app` → entry point is `main:app`)
- Accepts WSGI notation (`main:app`) as entry point
- Handles system scripts without `.py` extension (`unattended-upgrade-shutdown`)

**3. Node `package.json` parsing (`node_handler.go`)**

- Replaced `os.Stat()` + hardcoded `"unknown"` with actual `os.ReadFile` + `json.Unmarshal`
- Reads `name` and `version` fields from `package.json`
- Added `!proc.IsInContainer()` guard — container paths are inside the container's mount namespace and can't be read from the host (see "Problems encountered" below)

**4. Java jar version stripping (`java_handler.go`)**

- Extracted `jarVersionPatterns` as package-level var (shared between `stripJarVersion` and `extractNameFromJar`)
- Created `stripJarVersion()` — strips version without `cleanName` normalization (fingerprinting needs raw identity, not display normalization)
- Refactored `extractNameFromJar()` to call `cleanName(stripJarVersion(jarFile))`

**5. Rewritten `Fingerprint()` (`process.go`)**

Additive formula: `SHA256(language + systemd_unit + container_name + explicit_service_name + language_entry_point + working_directory)`. All available signals hashed; empty fields omitted. Parts after `language` sorted for determinism.

**6. Cache updates (`cache.go`, all handlers)**

- Added `SystemdUnit`, `ExplicitServiceName`, `WorkingDirectory`, `PackageName`, `ModulePath` to `ProcessCacheEntry`
- Updated cache write sites in Node and Python handlers
- Updated cache fast-path `Details` map reconstruction in Node and Python handlers

**7. Tests**

- `fingerprint_test.go` (NEW) — 13 cases: collision prevention (Python, cross-language), version-agnosticism (exe path, JAR), systemd unit, container name, explicit service name, package name, sorting stability
- `java_handler_test.go` (NEW) — `TestStripJarVersion` (8 cases), `TestExtractNameFromJarBackwardCompat` (5 cases)
- `python_handler_test.go` (NEW) — `TestExtractPythonInfo` (10 cases: `-m` module, `.py` script, uvicorn, gunicorn, celery, non-`.py` scripts, WSGI notation, empty args)
- `node_handler_test.go` (NEW) — `TestExtractNodeInfoPackageJson` (5 cases including scoped names, invalid JSON), `TestExtractNodeInfoNoPackageJson`

#### Problems encountered during implementation

**Problem 1: Container `package.json` inaccessible from host**

When Node.js runs inside a Docker container, `/proc/<pid>/cwd` returns a path inside the container's mount namespace (e.g., `/app`). Joining this with `package.json` gives `/app/package.json`, which doesn't exist on the host filesystem. Initial implementation tried to `os.ReadFile` this path and silently failed.

**Solution:** Added `!proc.IsInContainer()` guard around `package.json` read. For containerized processes, container name already provides identity via `ContainerInfo`, so `package.json` parsing is unnecessary.

**Problem 2: `extractNameFromJar("app-1.0.0.jar")` returns empty string**

`stripJarVersion("app-1.0.0.jar")` correctly produces `"app"`, but `extractNameFromJar` calls `cleanName("app")` which filters "app" as a generic/meaningless name. This caused a test failure.

**Solution:** This is correct behavior — `extractNameFromJar` is for service name display (where "app" is too generic), while `stripJarVersion` is for fingerprinting (where "app" is a valid identity component). The test expectation was updated, not the code.

**Problem 3: PM2 God Daemon not filtered — produces flickering fingerprints**

PM2's God Daemon rewrites its argv to `"PM2 v6.0.14: God Daemon (/home/user/.pm2)"`. Its exe resolves to the same `node` binary as the app workers. With the same cwd and no entry point, daemon and workers produced identical fingerprints. Since the daemon was not in `nodeLaunchers`, `isNodeLauncher()` didn't filter it, causing:
- Flickering service name, type, and fingerprint between discovery runs
- `settings[ss.Key]` collision between daemon and worker

**Solution (applied in earlier commit, before Phase 1):** Added `"pm2"` to `nodeLaunchers` map. Updated `isNodeLauncher()` to extract first word from `cmdArgs[0]` before lookup (handles both null-separated `"pm2", "start"` and space-joined `"PM2 v6.0.14: God Daemon ..."`).

**Problem 4: PM2 workers have space-joined cmdline (argv buffer overwrite)**

PM2 overwrites the process argv buffer at runtime. Instead of null-separated args (`node\0/path/to/index.js\0`), the cmdline becomes `node /path/to/index.js\0\0\0...`. The scanner produced a single-element `CmdArgs` slice, which meant all arg-based extraction (entry point, flags) failed.

**Solution (applied in earlier commit, before Phase 1):** Created `parseCmdline()` in `scanner.go` — detects single-arg cmdlines where the first word matches the exe name and re-splits on whitespace.

#### Real-world validation (PM2 cluster mode, 2 workers)

After Phase 1, `mw-agent ps` output with `pm2 start index.js --name browsebay-backend -i 2`:

```
SERVICE ID         SERVICE NAME           LANGUAGE   TYPE   PORTS   INSTANCES   INSTRUMENTED
a1b2c3d4e5f6g7h8   e-commerce-backend     node       pm2    -       1           -
```

**What works:**
- PM2 God Daemon correctly filtered by `isNodeLauncher()`
- Both workers detected with stable, identical fingerprint (correct — same workload class)
- Service name correctly derived from `package.json:name` or working directory
- Fingerprint stable across 4 consecutive runs

**What doesn't work (Phase 2 scope):**
- **PORTS: -** — workers don't hold the listen socket fd; port 3001 is on the daemon's fd table (see `process-manager-port-research.md`)
- **INSTANCES: 1** — `settings[ss.Key]` map collision silently drops Worker 2 (same Key `host-node-e-commerce-backend`). The downstream `DiscoverServices()` grouping works correctly but only receives 1 worker.

---

### Phase 2: Process Manager Support + Multi-Instance

**Depends on Phase 1** (completed) — fingerprints are now stable and collision-free.

Goal: Solve two remaining problems exposed by PM2 cluster mode testing:
1. Ports not detected on IPC-dispatch workers (port belongs to parent daemon)
2. Replica count wrong due to `settings[ss.Key]` map collision

Detailed research for both problems is in `docs/process-manager-port-research.md`.

#### Phase 2A: Parent Port Inheritance

When a worker process has no listen ports but its parent is a known process manager (PM2 God Daemon, Node cluster master), look up the parent's listen ports and attribute them to the worker.

**Key design question:** Should we detect PM2 daemon processes (currently filtered by `isNodeLauncher`) and show them in the service list? The daemon holds the listen socket, so showing it would make ports visible immediately. Options:

1. **Detect but mark as infrastructure** — let PM2 daemon through `Enrich()` as `serviceType = "pm2-daemon"`, show in `ps` with its port, exclude from instrumentation. Then link it to child workers.
2. **Keep filtering but inherit ports** — PM2 daemon stays invisible, but during `AttachListeners()` walk the parent PID chain and attribute daemon's ports to worker children. Daemon invisible, ports appear on workers.

Option 2 is cleaner long-term (users care about their app, not PM2 internals). Option 1 gives immediate visibility. Could do option 1 as quick win, then replace with option 2.

See `process-manager-port-research.md` for the full taxonomy of socket distribution patterns and proposed algorithm.

#### Phase 2B: Multi-Instance ServiceSettings

The `settings[ss.Key]` map in `report.go:93` silently drops replicas when multiple processes share the same Key (e.g., PM2 cluster workers both produce `host-node-e-commerce-backend`).

The fix is NOT to change the map key — that would break the backend API contract which expects one entry per workload class.

Instead, `ServiceSetting` should support multiple instances per entry:

1. **Add `Instances []InstanceInfo` to `ServiceSetting`** — each instance carries PID, ports, create time
2. **Accumulate in the map** — when `settings[ss.Key]` already exists, append to its Instances list instead of overwriting
3. **Report instance count** — backend gets one ServiceSetting per workload class with accurate instance metadata
4. **Aggregate ports** — union of all instance ports becomes the workload's port set

This aligns with how `ServiceEntry` in `services_api.go` already models workloads (fingerprint → multiple instances). The report layer should match.

#### Phase 2C: Fingerprint as Map Key

After multi-instance support is in place, migrate the settings map key from `ss.Key` to `ss.Fingerprint`:

1. `pkg/discovery/report.go` line 93: `settings[ss.Key] = *ss` → `settings[ss.Fingerprint] = *ss` (with accumulation)
2. `pkg/discovery/report.go` line 186 (`FilterInstrumentable`): `result[current.Key] = current` → `result[current.Fingerprint] = current`
3. `pkg/otelinject/services_api.go` lines 74-77: Add comment explaining Key fallback is for legacy stored settings

**No changes needed in:**
- Handler `ToServiceSetting()` methods — `Key` field stays, used by frontend
- `ApplyStoredInstrumentThis` — joins by `(ServiceName, Language)`, not map key
- `parseStoredSettings` — reads whatever keys backend has
- Bifrost frontend — uses `finalKey = s?.key || key` (prefers `Key` field inside JSON value)
- Bifrost backend — treats map keys as opaque

**Frontend key ping-pong (analyzed, harmless):** Frontend re-keys locally by `s.key` (human-readable) on read and sends that back on save. Agent re-keys by fingerprint on next sync. Harmless because merge logic joins by `(ServiceName, Language)` and agent replaces entire map on each POST.

### Pre-Phase 1 fixes (already in codebase)

These were applied during the investigation, before Phase 1 implementation:

- `scanner.go`: `parseCmdline()` detects PM2-style argv rewriting and re-splits
- `node_handler.go`: `"pm2"` added to `nodeLaunchers` map to filter God Daemon
- `node_handler.go`: `isNodeLauncher()` extracts first word from space-joined cmdlines
- `scanner_test.go`: Tests for `parseCmdline()` (6 cases)
- `inspector_test.go`: Tests for `isNodeLauncher()` (12 cases)
