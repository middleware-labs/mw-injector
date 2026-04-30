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

## Open Questions

1. **Gunicorn/Uvicorn verification:** Do our current port detection actually catch ports on Gunicorn workers (which inherit the fd via fork)? Need to test.

2. **Node cluster without PM2:** If someone uses `cluster.fork()` directly, the master process is NOT filtered by `isNodeLauncher()`. It looks like a normal Node app. Both master and workers would be detected, but only the master has the port. Should we deduplicate master+workers? Or let them coexist (same fingerprint, master has port, workers don't)?

3. **PM2 app name:** Should we try to read the PM2 app name (`~/.pm2/dump.pm2` contains the process list with names)? This would improve service name accuracy for PM2-managed apps. But it adds a dependency on PM2's internal file format.

4. **Multiple apps on one PM2 daemon:** PM2 can manage multiple apps. Each app gets its own workers, all children of the same daemon PID. The parent port inheritance needs to be smart about which ports belong to which app (possibly by matching the worker's entry point to the daemon's listening context). Or simpler: just attribute ALL parent ports to all children, and let the fingerprint grouping sort them out.

---

## Next Steps

### Phase 2A: Parent Port Inheritance

1. **Verify Gunicorn/Uvicorn port detection** — test with `gunicorn -w 4` and `uvicorn --workers 4` to confirm fork-inherited sockets are detected
2. **Design parent port inheritance** — detailed implementation plan for `AttachListeners()` modification
3. **PM2 name extraction** — investigate `~/.pm2/dump.pm2` format for optional name enrichment
4. **Test with Node cluster module directly** — verify behavior without PM2 wrapper

### Phase 2B: Multi-Instance ServiceSettings

5. **Add `Instances` to `ServiceSetting`** — accumulate workers instead of overwriting in `report.go`
6. **Update backend API contract** — ensure the report payload conveys instance count and per-instance metadata (PID, ports)
7. **Aggregate ports across instances** — union of all instance ports for the workload's OBI selector
