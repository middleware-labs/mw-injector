---
name: systemd-expert
description: |
  Systemd expertise for service management, unit files, and drop-in configuration.
  Use when: (1) Creating or modifying systemd unit files, (2) Working with drop-in overrides,
  (3) Managing service lifecycle (start/stop/restart), (4) Debugging service failures,
  (5) Configuring environment variables for services, (6) Understanding systemd security features.
---

# Systemd Expert Guide

## Unit File Locations

| Location | Purpose |
|----------|---------|
| `/usr/lib/systemd/system/` | Package-installed units (do not modify) |
| `/etc/systemd/system/` | Admin-created units and overrides |
| `/run/systemd/system/` | Runtime units (transient) |

Priority: `/etc` > `/run` > `/usr/lib`

## Drop-In Overrides

Override without editing original unit files:

```
/etc/systemd/system/{service-name}.service.d/
└── 10-middleware.conf
```

Naming convention: `NN-name.conf` where NN is priority (00-99).

### Drop-In Structure
```ini
[Service]
Environment="JAVA_TOOL_OPTIONS=-javaagent:/opt/middleware/agents/agent.jar"
Environment="OTEL_SERVICE_NAME=my-service"
```

### Creating Drop-Ins in Go
```go
func createDropIn(serviceName string, envVars map[string]string) error {
    dir := fmt.Sprintf("/etc/systemd/system/%s.service.d", serviceName)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return err
    }

    var content strings.Builder
    content.WriteString("[Service]\n")
    for k, v := range envVars {
        content.WriteString(fmt.Sprintf("Environment=\"%s=%s\"\n", k, v))
    }

    return os.WriteFile(
        filepath.Join(dir, "10-middleware.conf"),
        []byte(content.String()),
        0644,
    )
}
```

## Service Commands

```bash
# Reload after unit file changes
systemctl daemon-reload

# Service management
systemctl start|stop|restart|reload <service>
systemctl enable|disable <service>    # Boot persistence
systemctl status <service>
systemctl is-active <service>
systemctl is-enabled <service>

# View logs
journalctl -u <service> -f            # Follow logs
journalctl -u <service> --since today
journalctl -u <service> -n 100        # Last 100 lines

# List services
systemctl list-units --type=service
systemctl list-unit-files --type=service
```

## Common Unit Directives

### [Unit] Section
```ini
[Unit]
Description=My Java Application
Documentation=https://example.com/docs
After=network.target              # Start after network
Requires=postgresql.service       # Hard dependency
Wants=redis.service               # Soft dependency
```

### [Service] Section
```ini
[Service]
Type=simple                       # Main process is the service
Type=forking                      # For daemons that fork
Type=oneshot                      # Short-lived tasks
Type=notify                       # Service signals readiness

ExecStart=/usr/bin/java -jar /app/app.jar
ExecStop=/bin/kill -SIGTERM $MAINPID
ExecReload=/bin/kill -SIGHUP $MAINPID

User=appuser
Group=appgroup
WorkingDirectory=/app

Environment="JAVA_OPTS=-Xmx512m"
EnvironmentFile=/etc/default/myapp

Restart=on-failure
RestartSec=5s

# Resource limits
LimitNOFILE=65536
LimitNPROC=4096
```

### [Install] Section
```ini
[Install]
WantedBy=multi-user.target        # Start in multi-user mode
```

## Environment Variables

### Methods to Set
1. **Environment=** in unit file (single var)
2. **EnvironmentFile=** pointing to file
3. **Drop-in override** (preferred for modifications)

### EnvironmentFile Format
```bash
# /etc/default/myapp
JAVA_OPTS="-Xmx1g -Xms256m"
MW_API_KEY=xxx
```

### Clearing Inherited Variables
```ini
[Service]
Environment=           # Clears previous Environment= values
Environment="NEW=value"
```

## Security Directives

```ini
[Service]
# Filesystem
ProtectSystem=strict              # /usr, /boot read-only
ProtectHome=true                  # /home inaccessible
ReadOnlyPaths=/opt/middleware     # Specific read-only paths
ReadWritePaths=/var/log/myapp

# Capabilities
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true

# Namespacing
PrivateTmp=true                   # Isolated /tmp
PrivateDevices=true               # No access to physical devices
ProtectKernelTunables=true
ProtectControlGroups=true
```

## Tomcat Specifics

For Tomcat services, use CATALINA_OPTS instead of JAVA_TOOL_OPTIONS:

```ini
[Service]
Environment="CATALINA_OPTS=-javaagent:/opt/middleware/agents/agent.jar"
```

Drop-in for Tomcat:
```
/etc/systemd/system/tomcat.service.d/10-middleware.conf
```

## Debugging Service Issues

```bash
# Check why service failed
systemctl status <service>
journalctl -xe -u <service>

# Verify unit file syntax
systemd-analyze verify /etc/systemd/system/myapp.service

# Show effective configuration (with overrides)
systemctl cat <service>

# Show all applied drop-ins
systemctl show <service> --property=DropInPaths

# Check environment
systemctl show <service> --property=Environment
```

## Service Discovery

Find service for a process:

```go
func findServiceForPID(pid int) (string, error) {
    // Read /proc/<pid>/cgroup to find systemd slice
    data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
    if err != nil {
        return "", err
    }

    // Parse: "1:name=systemd:/system.slice/myapp.service"
    for _, line := range strings.Split(string(data), "\n") {
        if strings.Contains(line, ".service") {
            // Extract service name
        }
    }
    return "", nil
}
```

## After Modifying Units

Always run:
```bash
sudo systemctl daemon-reload
sudo systemctl restart <service>
```
