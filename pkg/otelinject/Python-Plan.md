# Manual OpenTelemetry Injection for Python Systemd Services

## Overview

Unlike Java or Node.js, the OpenTelemetry injector for Python is **opt-in** and does not ship with a default agent. The injector (`libotelinject.so`) successfully modifies the environment (setting `PYTHONPATH`), but Python requires an additional bootstrap mechanism (`sitecustomize.py`) to actually initialize the instrumentation.

## Prerequisites

* **Injector Installed**: `libotelinject.so` present (typically in `/usr/lib/opentelemetry/`).
* **Target Application**: A Python application running via Systemd (e.g., Flask).
* **Root Access**: Required to create the agent directory and Systemd drop-in files.

---

## Step 1: Create the "Universal" Agent Directory

The injector requires a specific directory structure (`glibc`/`musl`) to handle dynamic libc detection.

```bash
# 1. Create the base directory structure
sudo mkdir -p /opt/otel-python-agent/glibc

# 2. Install OpenTelemetry dependencies into this specific folder
# Note: --target is critical to isolate these libs from the system python
sudo pip3 install --target /opt/otel-python-agent/glibc \
    opentelemetry-distro \
    opentelemetry-exporter-otlp \
    opentelemetry-instrumentation-flask \
    opentelemetry-instrumentation-requests

```

## Step 2: The "Missing Link" (Bootstrapper)

The injector only sets `PYTHONPATH`. To force Python to load OTel on startup, we must create a `sitecustomize.py` file, which Python automatically executes if found in its path.

**File:** `/opt/otel-python-agent/glibc/sitecustomize.py`

```python
import os
import sys

# Debug: Confirm bootstrapping is triggering
print("[OTel-Bootstrap] Loading OpenTelemetry auto-instrumentation...", file=sys.stderr)

try:
    # Trigger the standard OTel auto-instrumentation hook
    from opentelemetry.instrumentation.auto_instrumentation import sitecustomize
except ImportError as e:
    print(f"[OTel-Bootstrap] Failed to load OTel: {e}", file=sys.stderr)
except Exception as e:
    print(f"[OTel-Bootstrap] Error during OTel init: {e}", file=sys.stderr)

```

**Permissions:** Ensure the service user (e.g., `naman47`) can read this file.

```bash
sudo chmod -R 755 /opt/otel-python-agent

```

## Step 3: Configure Systemd (Drop-In)

We use a **Systemd Drop-In** file to inject the configuration without modifying the original service file. This is the production-grade approach.

**File:** `/etc/systemd/system/flask-book-api.service.d/otel-inject.conf`

```ini
[Service]
# --- 1. The Injector Mechanism ---
# Loads the Zig library into the process address space
Environment="LD_PRELOAD=/usr/lib/opentelemetry/libotelinject.so"

# --- 2. The Python Agent Path ---
# Points to the PARENT directory. The injector appends '/glibc' automatically.
Environment="PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX=/opt/otel-python-agent"

# --- 3. OpenTelemetry Exporter Config ---
Environment="OTEL_SERVICE_NAME=my-python-service"
Environment="OTEL_EXPORTER_OTLP_ENDPOINT=https://msehd.middleware.io:443"
Environment="OTEL_EXPORTER_OTLP_HEADERS=Authorization=xzdyagardggtppccwqfgqsqeocjflbddytwg"
Environment="OTEL_TRACES_EXPORTER=otlp"
Environment="OTEL_METRICS_EXPORTER=otlp"
Environment="OTEL_LOGS_EXPORTER=otlp"

# --- 4. Debugging ---
# Critical for verifying the injector loaded successfully
Environment="OTEL_INJECTOR_LOG_LEVEL=info"

```

**Apply Changes:**

```bash
sudo systemctl daemon-reload
sudo systemctl restart flask-book-api

```

---

## Findings & Considerations

### 1. The "Dependency Hell" Risk

* **Finding:** The injector warns about dependency conflicts because it forces the agent's libraries into the application's path.
* **Observed:** We saw `pip` warnings (e.g., `yt-dlp` conflict) during installation.
* **Mitigation:** The `--target` flag isolates the OTel libs from the global system Python, but conflicts can still occur if the application uses different versions of `protobuf`, `grpc`, or `requests` than the agent.

### 2. Systemd vs. `LD_PRELOAD`

* **Finding:** Systemd often strips `LD_PRELOAD` for security when switching users (`User=naman47`), causing the injection to fail silently.
* **Workaround:** We verified the injector works by briefly forcing it globally via `/etc/ld.so.preload`.
* **Resolution:** If the drop-in `LD_PRELOAD` is ignored in strict environments, the fallback is to use the **Wrapper Method** (setting `ExecStart=/opt/.../opentelemetry-instrument python app.py`) instead of the injector library.

### 3. The `sitecustomize.py` Requirement

* **Discovery:** The injector logic (written in Zig) only modifies environment variables (`PYTHONPATH`). It does **not** execute Python code.
* **Implication:** Without `sitecustomize.py`, the libraries are present but dormant. The bootstrapper is mandatory for "zero-code" instrumentation to actually start.

### 4. Global Preload Danger

* **Critical Warning:** We briefly used `/etc/ld.so.preload` for debugging. This caused shell errors (`ERROR: ld.so: object ... cannot be preloaded`) because the library was loaded into *every* process, including those with architecture mismatches or different libc versions.
* **Rule:** **Never** leave `/etc/ld.so.preload` active on a production system. Always scope injection to the specific Systemd service.
