# Process Manager Research: Port Inheritance, Worker Detection, and Parent-Child Relationships

## The Core Problem

When a process manager runs in **multi-worker mode** (cluster, prefork, multi-process), it creates a parent-child architecture where:

1. The **parent/master** process binds the listen socket (opens the port)
2. **Worker processes** receive connections via IPC, fd inheritance, or SO_REUSEPORT — but may NOT hold the listen socket fd themselves
3. Our discovery detects the **workers** (they're the actual language processes), but port detection finds nothing because the listen socket belongs to the parent PID

This means the user sees their service with `PORTS: -` even though the service is actively listening on a well-known port.

---

## Observed Evidence

### PM2 Cluster Mode (Node.js)

Setup: `pm2 start index.js --name browsebay-backend -i 2`

```
PID 194707 — PM2 God Daemon
  exe: /home/hardik/.nvm/versions/node/v22.17.0/bin/node
  cmdline: "PM2 v6.0.14: God Daemon (/home/hardik/.pm2)"
  cwd: /home/hardik/hardik/browse-bay/backend
  fd 3 → socket:[11511099]  ← THIS IS THE PORT 3001 LISTEN SOCKET

PID 498849 — Worker 1
  exe: /home/hardik/.nvm/versions/node/v22.17.0/bin/node
  cmdline: "node /home/hardik/hardik/browse-bay/backend/index.js"
  cwd: /home/hardik/hardik/browse-bay/backend
  ppid: 194707
  fd 3 → socket:[11471394]  ← IPC unix socket to daemon (NOT the listen socket)

PID 498856 — Worker 2
  exe: /home/hardik/.nvm/versions/node/v22.17.0/bin/node
  cmdline: "node /home/hardik/hardik/browse-bay/backend/index.js"
  cwd: /home/hardik/hardik/browse-bay/backend
  ppid: 194707
  fd 3 → socket:[11505168]  ← IPC unix socket to daemon (different from worker 1)
```

**Socket ownership:**
- `/proc/194707/net/tcp6` shows port 3001 (0x0BB9) with inode 11511099
- `ss -tlpn` shows port 3001 owned by `"PM2 v6.0.14: Go",pid=194707,fd=3`
- Workers share the same `/proc/<pid>/net/tcp6` view (same network namespace) but their fd table does NOT include the listen socket inode
- Workers connect to daemon via paired unix sockets: worker fd 3 (11471394) ↔ daemon fd 24 (11471393)

**PM2 cluster mode mechanism:** PM2 uses Node.js `cluster` module internally. The master calls `net.createServer().listen()` which binds the port. Workers are forked via `child_process.fork()`. When a worker calls `server.listen()`, Node's cluster module intercepts it and instead sets up an IPC channel. The master accepts connections and round-robins them to workers via IPC message passing (sending the fd). Workers never call `bind()` themselves.

**Our detection result:**
- PM2 God Daemon: filtered by `isNodeLauncher()` (correct — it's not the app)
- Worker 1 (498849): detected, fingerprint + service name correct, but PORTS: -
- Worker 2 (498856): detected, same fingerprint, PORTS: -
- Port 3001: invisible to the user

### Key Insight

The listen socket inode (11511099) appears in `/proc/498849/net/tcp6` but NOT in `/proc/498849/fd`. Our `AttachListeners()` implementation correctly cross-references both — it reads the net table for listening sockets, then checks if the process's fd table contains the inode. Since workers don't have the inode in their fd table, no match, no port.

---

## Research: How Different Process Managers Handle Sockets

### 1. PM2 Cluster Mode (Node.js)

**Mechanism:** Node.js `cluster` module. Master binds, workers get connections via IPC.

**Socket ownership:** Master only. Workers have IPC unix sockets, not the listen socket.

**Detection pattern:**
- Master: `cmdline` starts with "PM2" — filtered by `isNodeLauncher()`
- Workers: `cmdline` = "node <entry_point>", ppid = PM2 daemon PID
- Port visible on master's fd table, not workers'

**PM2 fork mode** (non-cluster): Each worker binds its own port. Port detection works normally. But fork mode can't share a single port across instances.

### 2. Gunicorn Prefork (Python)

**Mechanism:** Master process calls `bind()` + `listen()`, then `fork()`s worker processes. Workers inherit the listen socket fd via fork.

**Socket ownership:** Master AND workers (fd inherited across fork). The listen socket inode appears in BOTH the master's and workers' fd tables.

**Detection pattern:**
- Master: `cmdline` = "gunicorn myapp:app -w 4" — currently detected as a Python process
- Workers: `cmdline` = "gunicorn: worker [myapp:app]" (Gunicorn rewrites argv)
- Port should be visible on workers because they inherit the fd

**TODO:** Verify — does our port detection actually work for Gunicorn workers? The fd inheritance should mean yes, but Gunicorn may close and re-accept in a way that changes this.

### 3. Uvicorn Multi-worker (Python)

**Mechanism:** Uses `multiprocessing` to spawn workers. Master binds the socket, workers inherit via fork (like Gunicorn).

**Socket ownership:** Master AND workers (fd inherited).

**Detection pattern:**
- Master: `cmdline` = "python -m uvicorn main:app --workers 4"
- Workers: `cmdline` = "python -m uvicorn main:app --workers 4" (same cmdline, different PID)
- Workers currently filtered by `isSubProcess` check for `multiprocessing.spawn` in cmdline — **need to verify this doesn't filter uvicorn workers**

### 4. Node.js `cluster` Module (without PM2)

**Mechanism:** Same as PM2 cluster mode but without the daemon layer. `cluster.fork()` creates workers. Master binds, distributes via IPC.

**Socket ownership:** Master only (same as PM2).

**Detection pattern:**
- Master: `cmdline` = "node server.js" — detected as a normal Node process
- Workers: `cmdline` = "node server.js" — same cmdline, different PID, ppid = master
- Port on master's fd table only

### 5. systemd Socket Activation

**Mechanism:** systemd creates the listen socket and passes it as fd 3 to the service process via `LISTEN_FDS` env var.

**Socket ownership:** The service process (fd 3 inherited from systemd). systemd releases its reference after passing it.

**Detection pattern:** Port detection works because the service process holds the fd. No issue here.

### 6. Docker / Containerized Processes

**Mechanism:** `docker-proxy` (userland proxy) or iptables DNAT handles port forwarding. The container process binds inside its network namespace.

**Socket ownership:** Container process owns the socket in its netns. `docker-proxy` owns the host-side socket.

**Detection pattern:** Our port detection reads `/proc/<pid>/net/tcp*` which reflects the container's netns if the process is in one. This should work correctly for the container's internal ports. Host-mapped ports are on `docker-proxy`, not the container process.

### 7. Java Application Servers (Tomcat, WildFly, etc.)

**Mechanism:** Single JVM process, multiple threads. The main thread binds the port; handler threads accept from the same server socket.

**Socket ownership:** The JVM process (single PID) owns the socket. Threads share the fd table.

**Detection pattern:** Port detection works because it's one PID with the listen socket in its fd table.

---

## Taxonomy of Socket Distribution Patterns

| Pattern | Master holds socket | Workers hold socket | Examples |
|---------|-------------------|-------------------|----------|
| **IPC dispatch** | Yes | No (IPC sockets only) | PM2 cluster, Node cluster module |
| **Fork inheritance** | Yes | Yes (inherited fd) | Gunicorn, Uvicorn, Apache prefork |
| **SO_REUSEPORT** | Per-worker | Per-worker (each binds) | Envoy, some Go servers, nginx (optional) |
| **Single process** | Yes | N/A (threads, not processes) | Tomcat, WildFly, Spring Boot, Express (single) |
| **Socket activation** | Systemd passes fd | Service holds fd | systemd socket-activated services |

**Only the "IPC dispatch" pattern is broken for us.** Fork inheritance and SO_REUSEPORT both put the socket in the worker's fd table. Single process and socket activation also work.

---

## Proposed Solution: Parent Port Inheritance

When a worker process has no listen ports but its parent is a known process manager, look up the parent's listen ports and attribute them to the worker.

### Algorithm

```
For each discovered process with no listen ports:
  1. Read ppid from /proc/<pid>/status
  2. Check if ppid is a known process manager pattern:
     - PM2 God Daemon (cmdline starts with "PM2")
     - Node cluster master (same exe as worker, has listen sockets)
     - Other process managers (future)
  3. If yes, read the parent's listen ports via /proc/<ppid>/fd + /proc/<ppid>/net/tcp*
  4. Attribute those ports to the worker
```

### Design Considerations

1. **Only for IPC-dispatch pattern:** Don't do this for fork-inheritance (workers already have the port) or single-process (no workers).

2. **Parent identification:** How do we know the parent is a process manager? Options:
   - Check if ppid's cmdline matches known patterns (PM2, cluster master)
   - Check if ppid's exe is the same as the worker's exe (Node cluster pattern)
   - Check if the worker has IPC unix sockets to the parent (definitive proof of IPC dispatch)

3. **Scope:** Should this run during `AttachListeners()` (post-enrichment batch) or during each handler's `Enrich()`?
   - `AttachListeners()` already batch-processes all ports and groups by netns. Adding parent lookup here keeps the port logic consolidated.
   - The parent PID is already available from `/proc/<pid>/status` (read during enrichment as `ParentPID`).

4. **Performance:** Reading the parent's fd table is one extra `/proc` read per portless worker. Acceptable since it only triggers for processes with no ports and a process-manager parent.

5. **PM2-specific:** For PM2, we know the daemon PID (it's the ppid of all workers). We could read the daemon's ports once and apply to all its children. This is the most common case.

### What This Does NOT Solve

- **Service name from PM2:** PM2 has its own naming (`--name browsebay-backend`). This name is in the PM2 config/process list, not in `/proc`. We'd need to read `~/.pm2/dump.pm2` or call `pm2 jlist` to get it. Out of scope for this research.
- **PM2 fork mode port conflicts:** In fork mode, each worker tries to bind the same port and fails (unless using SO_REUSEPORT). This is a user misconfiguration, not our problem.

---

## Bug: `settings[ss.Key]` Map Collision Drops Replicas

### Problem

In `report.go:85-96`, `GetAgentReportValue()` builds a map keyed by `ServiceSetting.Key`:

```go
settings := map[string]ServiceSetting{}
for lang, procs := range allProcs {
    handler := d.handlerRegistry.ForLanguage(lang)
    for _, proc := range procs {
        if ss := handler.ToServiceSetting(proc); ss != nil {
            settings[ss.Key] = *ss  // line 93 — OVERWRITES duplicates
        }
    }
}
```

`ServiceSetting.Key` is `"{serviceType}-{language}-{serviceName}"` (e.g., `host-node-e-commerce-backend`). When a process manager runs multiple workers of the same app, all workers produce the same Key — same type, same language, same service name. The second worker silently overwrites the first in the map.

### Observed Behavior (PM2 cluster, 2 workers)

```
Worker 1 (PID 498849): Key = "host-node-e-commerce-backend"  → stored
Worker 2 (PID 498856): Key = "host-node-e-commerce-backend"  → overwrites Worker 1
```

Result: `mw-agent ps` shows 1 instance instead of 2. The backend API report contains only Worker 2's data.

### Why This Matters

The downstream `DiscoverServices()` in `services_api.go` groups by fingerprint and accumulates instances into `ServiceEntry.Instances[]`. But by that point, only one instance survived the lossy intermediate map. The instance grouping logic works correctly — it's just starved of input.

### Phase 2 Fix: Multi-Instance ServiceSettings

The fix is NOT to change the map key (e.g., appending PID or fingerprint) — that would break the backend API contract which expects one entry per workload class.

Instead, `ServiceSetting` should support multiple instances per entry:

1. **Add `Instances []InstanceInfo` to `ServiceSetting`** — each instance carries PID, ports, create time
2. **Accumulate in the map** — when `settings[ss.Key]` already exists, append to its Instances list instead of overwriting
3. **Report instance count** — backend gets one ServiceSetting per workload class with accurate instance metadata
4. **Aggregate ports** — union of all instance ports becomes the workload's port set

This aligns with how `ServiceEntry` in `services_api.go` already models workloads (fingerprint → multiple instances). The report layer should match.

---

## PM2 Cluster Mode vs Fork Mode

PM2 supports two execution modes. Understanding the difference is critical for port detection:

### Cluster Mode (`exec_mode: "cluster"`)

Uses Node.js's built-in `cluster` module. The PM2 God Daemon acts as the cluster primary:

1. When your app calls `server.listen(3000)`, the cluster module **intercepts** that call in the worker
2. Worker sends an IPC message to the God Daemon (cluster primary)
3. Daemon creates a `RoundRobinHandle`, which calls `bind()` + `listen()` at the OS level
4. Workers **never** call `bind()` themselves — they receive connections via IPC message passing
5. All workers share the same port via the daemon

**Port ownership:** Daemon only. Workers have IPC unix sockets, not listen sockets.

Source: [Node.js Cluster Module](https://nodejs.org/api/cluster.html), [node/lib/internal/cluster/round_robin_handle.js](https://github.com/nodejs/node/blob/main/lib/internal/cluster/round_robin_handle.js)

### Fork Mode (`exec_mode: "fork"`, default)

Uses `child_process.fork()`. Each instance is fully independent:

1. Each worker calls `bind()` + `listen()` directly
2. If two workers try to bind the same port, the second one **fails** (unless `SO_REUSEPORT`)
3. Each worker owns its own listen socket
4. Port detection works normally — each worker has the socket in its fd table

**Port ownership:** Each worker individually. Port detection works out of the box.

### Why This Matters for Us

Only **cluster mode** has the port inheritance problem. Fork mode workers hold their own sockets. Our parent port inheritance logic should only activate for cluster mode workers.

---

## PM2 Port Visibility — Verified Limitation

**PM2 has zero port visibility.** It has no native port field, no API to query listening ports, and no way to know what your app passed to `server.listen()`.

- [GitHub issue #428](https://github.com/Unitech/pm2/issues/428) (2014, closed unresolved): "PM2 list doesn't show used port"
- [GitHub issue #4907](https://github.com/Unitech/pm2/issues/4907) (2020, still open): "pm2 list doesn't shows the service ports"

The `axm_monitor` in PM2's telemetry tracks HTTP latency/rate but NOT the port. The IPC bus events don't carry it either.

**How PM2 "knows" a port:** Only if you explicitly declare `PORT` in the `env` block of your ecosystem config. This shows up in `pm2_env.PORT` via `pm2 jlist`. PM2 also supports `increment_var: 'PORT'` to auto-increment per instance (fork mode).

**What production teams do:** Use ecosystem config as the canonical port map, use nginx `proxy_pass` as the registry, or have apps self-report at startup.

### `pm2 jlist` as Enrichment Source

Shelling out to `pm2 jlist` gives structured JSON with useful fields per worker:

| Field | Value | Use |
|---|---|---|
| `pid` | Worker PID | Match to discovered processes |
| `name` | PM2 app name | Better service name than heuristic |
| `pm2_env.exec_mode` | `"cluster_mode"` or `"fork_mode"` | Only do port inheritance for cluster |
| `pm2_env.instances` | Expected count | Show "2/2 instances" |
| `pm2_env.pm_exec_path` | Entry point path | Cross-check with discovery |
| `pm2_env.NODE_APP_INSTANCE` | 0, 1, 2... | Instance ordinal |

**Not available in jlist:** Port number (confirmed — PM2 doesn't track it).

Call `pm2 jlist` once during discovery if we detect any PM2 workers (ppid matches a filtered PM2 daemon PID). Cache the result for that discovery cycle. Parse JSON, index by PID.

---

## Multi-App PM2 Daemon Port Attribution Problem

### Architecture

There is **one PM2 God Daemon per `PM2_HOME`** (default `~/.pm2`). All apps share it.

Source: [PM2 Multiple Runtime](https://pm2.io/docs/runtime/features/multiple-pm2/), confirmed via GitHub issues [#2784](https://github.com/Unitech/pm2/issues/2784), [#2987](https://github.com/Unitech/pm2/issues/2987)

### The Problem (Example)

```bash
pm2 start /home/hardik/browse-bay/backend/index.js --name browsebay -i 2    # port 3001
pm2 start /home/hardik/chat-app/server.js --name chatapp -i 1                # port 4000
```

Process tree:
```
PID 194707 — PM2 God Daemon
  fd 3 → TCP *:3001 (LISTEN)    ← browsebay's port
  fd 5 → TCP *:4000 (LISTEN)    ← chatapp's port
  │
  ├── PID 498849 — browsebay worker 0  (fingerprint=aaa111)  PORTS: -
  ├── PID 498856 — browsebay worker 1  (fingerprint=aaa111)  PORTS: -
  └── PID 499100 — chatapp worker 0    (fingerprint=bbb222)  PORTS: -
```

If we naively inherit ALL parent ports to ALL children:
- browsebay workers → `[3001, 4000]` — port 4000 is wrong
- chatapp worker → `[3001, 4000]` — port 3001 is wrong

**Why it's unsolvable from /proc:** Both listen sockets are in the daemon's fd table (PID 194707). The kernel doesn't record which socket was opened by which app's cluster master code. It's all one process, one event loop.

**Why `pm2 jlist` doesn't help:** It has `pid`, `name`, `exec_mode`, but NO port field. PM2 doesn't track ports (see above).

### Solution: Conditional Inheritance

**Single app (common case):** If all PM2 cluster workers under the same daemon share the same fingerprint, inherit ALL parent ports. This is safe — all ports belong to the same app.

**Multiple apps (uncommon case):** If workers under the same daemon have different fingerprints, we cannot determine which port belongs to which app. Two options:

1. **Skip port inheritance** — workers show `PORTS: -`. Honest but unhelpful.
2. **Use OBI's fallback selectors** — OBI can instrument without ports using `languages` + `cmd_args` matching. Log a warning that port-based OBI selector is not available, fall back to cmd_args selector for disambiguation.

Option 1 is safer. OBI already handles portless instrumentation gracefully (logs a warning, matches by language). Users can manually specify ports via the planned user-override feature (see CLAUDE.md TODO).

---

## Resolved Questions

1. **Gunicorn/Uvicorn port detection:** Fork-inherited sockets (Gunicorn prefork, Uvicorn multiprocessing) should be detected because workers inherit the listen socket fd via `fork()`. The fd appears in both parent's and child's fd table. **TODO:** Verify empirically.

2. **Node cluster without PM2:** The master process is not filtered by `isNodeLauncher()` — it looks like a normal Node app. Master and workers coexist with the same fingerprint. Master has the port, workers don't. After Phase 2B (multi-instance accumulation), the port will aggregate correctly via fingerprint grouping — master's port becomes the workload's port. No deduplication needed.

3. **PM2 app name:** Use `pm2 jlist` (structured JSON API) instead of `~/.pm2/dump.pm2` (internal file format). jlist provides PID-to-name mapping, exec_mode, instance count. Call once per discovery cycle if PM2 workers detected.

4. **Multiple apps on one PM2 daemon:** Solved via conditional inheritance (see above). Single-fingerprint = inherit all ports. Multi-fingerprint = skip inheritance, fall back to OBI language+cmd_args matching.

---

## OBI Without Ports

OBI does NOT require ports for instrumentation. The `OBISelector` has multiple optional fields:

```go
type OBISelector struct {
    Name           string `yaml:"name,omitempty"`
    OpenPorts      string `yaml:"open_ports,omitempty"`     // optional
    ExePath        string `yaml:"exe_path,omitempty"`       // optional
    CmdArgs        string `yaml:"cmd_args,omitempty"`       // optional — used for Java jar/main class
    Languages      string `yaml:"languages,omitempty"`      // always set
    ContainersOnly bool   `yaml:"containers_only,omitempty"`
}
```

When no ports are discovered, `buildOBISelector` builds a selector with just `Languages` (and `CmdArgs` for Java). It logs a warning but doesn't fail. This means PM2 cluster workers CAN be instrumented even without port detection — OBI matches by language and command line patterns.

---

## Updated Implementation Plan

### Phase 2 Implementation Order

**Phase 2A: Multi-Instance ServiceSettings + Fingerprint Map Key** (2B+2C combined)

These are tightly coupled. Change together:

1. Add `Instances []InstanceInfo` to `ServiceSetting` — each instance carries PID, create time
2. Change `report.go` map key from `ss.Key` to `ss.Fingerprint`
3. Accumulate instances: when `settings[ss.Fingerprint]` already exists, append to its `Instances` list instead of overwriting
4. Aggregate `Listeners` across all instances (union of ports)
5. Update `FilterInstrumentable` to use fingerprint as map key
6. `DiscoverServices()` in `services_api.go` already groups by fingerprint — it will now receive correct instance counts from the report layer

**Backend impact: NONE.** The backend stores `auto_instrumentation_settings` as opaque JSONB. Map key change is invisible. New `instances` field is additive JSON. `ApplyStoredInstrumentThis` joins by `(ServiceName, Language)`, not map key. `parseStoredSettings` iterates whatever keys exist. Frontend re-keys by `s.key || key` (reads value's `key` field).

**CLI impact:** `ps` command shows correct instance count and aggregated ports. `ps -a` shows per-instance detail. Instrument/uninstrument by service name or fingerprint works unchanged — `findService()` already matches by name or fingerprint.

**Phase 2B: Parent Port Inheritance**

After multi-instance is working, add port inheritance for IPC-dispatch workers:

1. During `AttachListeners()`, for each portless worker:
   - Read `ppid` from `/proc/<pid>/status`
   - Check if ppid matches a known PM2 daemon (cmdline starts with "PM2")
   - If yes, check if all workers under that daemon share the same fingerprint
   - If single fingerprint: read parent's listen ports, attribute to worker
   - If multiple fingerprints: skip inheritance, log warning
2. Optionally call `pm2 jlist` for enrichment:
   - Confirm `exec_mode: "cluster_mode"` (skip fork mode workers — they have their own ports)
   - Get PM2 app name for service name enrichment
   - Get expected instance count

**Phase 2C: Frontend Enhancement** (separate phase, Bifrost changes)

Minor Bifrost frontend update to display the new `instances` array:

1. **Linux services grid** (`bifrost/front/.../linux-auto-instrumentation/`):
   - Show instance count from `instances.length` in the services table
   - Optionally show per-instance PIDs in an expanded detail row
   - Show aggregated ports from all instances
2. **No backend changes needed** — JSONB pass-through already handles the new fields
3. Bifrost code paths:
   - Frontend: `/home/hardik/work/mw/middleware/bifrost/front/src/views/modules/installation-v2/pages/agent/linux-auto-instrumentation/`
   - Backend: `/home/hardik/work/mw/middleware/bifrost/app` (no changes expected)
