---
name: docker-expert
description: |
  Docker and container expertise for Go applications. Covers container runtimes, Dockerfile best practices,
  Docker API, container instrumentation, and debugging containerized apps.
  Use when: (1) Working with Docker containers or images, (2) Writing code that interacts with container runtimes,
  (3) Debugging container-related issues, (4) Implementing container discovery or instrumentation,
  (5) Questions about Docker Compose, container networking, or volume mounts.
---

# Docker Expert Guide

## Container Runtime Detection

Detect container runtime from cgroup:

```go
// Read /proc/<pid>/cgroup to detect container
func getContainerID(pid int) (string, string, error) {
    data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
    if err != nil {
        return "", "", err
    }

    // Parse cgroup v1: "10:memory:/docker/<container-id>"
    // Parse cgroup v2: "0::/system.slice/docker-<container-id>.scope"
    for _, line := range strings.Split(string(data), "\n") {
        if strings.Contains(line, "docker") {
            // Extract container ID (64 hex chars)
        }
        if strings.Contains(line, "containerd") {
            // containerd runtime
        }
    }
    return "", "", nil
}
```

Supported runtimes: Docker, Podman, containerd, cri-o

## Docker API via CLI

Prefer `docker` CLI over API for simplicity:

```go
// Get container name
cmd := exec.Command("docker", "inspect", "--format", "{{.Name}}", containerID)
output, err := cmd.Output()

// List running containers
cmd := exec.Command("docker", "ps", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}")

// Execute in container
cmd := exec.Command("docker", "exec", containerID, "cat", "/proc/1/cmdline")
```

## Docker Compose Detection

```go
// Check for compose labels
cmd := exec.Command("docker", "inspect", "--format",
    "{{index .Config.Labels \"com.docker.compose.project\"}}", containerID)

// Get compose service name
cmd := exec.Command("docker", "inspect", "--format",
    "{{index .Config.Labels \"com.docker.compose.service\"}}", containerID)
```

Key labels:
- `com.docker.compose.project` - Project name
- `com.docker.compose.service` - Service name
- `com.docker.compose.container-number` - Replica number

## Container Instrumentation Patterns

### Volume Mount Approach
Mount agent JAR into container:

```bash
docker run -v /opt/middleware/agents:/opt/agents:ro \
  -e JAVA_TOOL_OPTIONS="-javaagent:/opt/agents/middleware-javaagent.jar" \
  my-java-app
```

### Docker Compose Override
```yaml
# docker-compose.override.yml
services:
  my-service:
    volumes:
      - /opt/middleware/agents:/opt/agents:ro
    environment:
      - JAVA_TOOL_OPTIONS=-javaagent:/opt/agents/middleware-javaagent.jar
```

## Container Networking

```go
// Get container IP
cmd := exec.Command("docker", "inspect", "--format",
    "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerID)

// Get exposed ports
cmd := exec.Command("docker", "inspect", "--format",
    "{{range $p, $conf := .NetworkSettings.Ports}}{{$p}} {{end}}", containerID)
```

## Debugging Containers

```bash
# Enter running container
docker exec -it <container> /bin/sh

# View logs
docker logs -f <container>

# Check resource usage
docker stats <container>

# Inspect full config
docker inspect <container> | jq '.[0].Config'

# Check mounted volumes
docker inspect --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{"\n"}}{{end}}' <container>
```

## Common Issues

### Permission Denied in Container
- Check `:ro` vs `:rw` volume mount
- Verify file ownership matches container user
- Use `--user` flag or set appropriate permissions

### Container Not Found
- Container might have restarted (new ID)
- Use container name instead of ID when possible
- Implement retry with exponential backoff

### Java Agent Not Loading
- Verify volume mount path exists in container
- Check JAVA_TOOL_OPTIONS is set correctly
- Ensure agent JAR is readable (chmod 644)

## Go Docker Client Library

When CLI is insufficient, use official client:

```go
import "github.com/docker/docker/client"

cli, err := client.NewClientWithOpts(client.FromEnv)
containers, err := cli.ContainerList(ctx, types.ContainerListOptions{})
```

Only use when needed - CLI is simpler for most operations.
