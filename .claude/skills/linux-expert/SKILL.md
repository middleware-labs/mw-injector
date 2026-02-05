---
name: linux-expert
description: |
  Linux internals expertise for Go system programming. Covers /proc filesystem, cgroups, process management,
  file permissions, and system calls.
  Use when: (1) Working with /proc filesystem, (2) Parsing cgroups for container detection, (3) Managing file
  permissions and ownership, (4) Process introspection and management, (5) Debugging permission or access issues,
  (6) Understanding Linux process hierarchies or namespaces.
---

# Linux Expert Guide

## /proc Filesystem

### Process Information
```go
// /proc/<pid>/cmdline - Command line arguments (null-separated)
data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
args := strings.Split(string(data), "\x00")

// /proc/<pid>/exe - Symlink to executable
exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))

// /proc/<pid>/cwd - Current working directory
cwd, _ := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))

// /proc/<pid>/environ - Environment variables (null-separated)
data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
envs := strings.Split(string(data), "\x00")

// /proc/<pid>/status - Process status (parsed key-value)
// Contains: Name, State, Pid, PPid, Uid, Gid, etc.

// /proc/<pid>/fd/ - Open file descriptors (directory of symlinks)
```

### System-wide Information
```
/proc/loadavg      - Load averages
/proc/meminfo      - Memory statistics
/proc/cpuinfo      - CPU information
/proc/mounts       - Mounted filesystems
/proc/net/tcp      - TCP connections
```

## Cgroups

### cgroup v1 (legacy)
```
/proc/<pid>/cgroup format:
hierarchy-ID:controller-list:cgroup-path

Example:
12:memory:/docker/abc123def456
11:cpu,cpuacct:/docker/abc123def456
```

### cgroup v2 (unified)
```
/proc/<pid>/cgroup format:
0::<cgroup-path>

Example:
0::/system.slice/docker-abc123def456.scope
```

### Detecting Container from cgroup
```go
func isContainerized(pid int) (bool, string) {
    data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
    if err != nil {
        return false, ""
    }

    content := string(data)

    // Docker patterns
    if strings.Contains(content, "/docker/") {
        // Extract 64-char hex container ID
    }
    if strings.Contains(content, "docker-") && strings.Contains(content, ".scope") {
        // cgroup v2 docker pattern
    }

    // Other runtimes
    // containerd: /system.slice/containerd-<id>.scope
    // podman: /machine.slice/libpod-<id>.scope
    // cri-o: /crio-<id>.scope

    return false, ""
}
```

## File Permissions

### Permission Bits
```
Owner  Group  Other
rwx    rwx    rwx
421    421    421

0644 = rw-r--r-- (files)
0755 = rwxr-xr-x (directories, executables)
0600 = rw------- (sensitive files)
```

### Go Permission Operations
```go
// Check if file is readable by all
info, _ := os.Stat(path)
mode := info.Mode()
worldReadable := mode&0004 != 0

// Set permissions
os.Chmod(path, 0644)

// Set ownership (requires root)
os.Chown(path, uid, gid)

// Check effective access
err := unix.Access(path, unix.R_OK)
```

## Process Management

### Process States
```
R - Running
S - Sleeping (interruptible)
D - Disk sleep (uninterruptible)
Z - Zombie
T - Stopped
```

### Finding Processes
```go
// List all PIDs
entries, _ := os.ReadDir("/proc")
for _, e := range entries {
    if pid, err := strconv.Atoi(e.Name()); err == nil {
        // Valid PID directory
    }
}

// Using gopsutil (preferred)
import "github.com/shirou/gopsutil/v4/process"
procs, _ := process.Processes()
```

### Process Relationships
```go
// Get parent PID from /proc/<pid>/status
// PPid: 1234

// Get child PIDs from /proc/<pid>/task/<tid>/children
```

## User/Group Resolution

```go
import "os/user"

// Get user by UID
u, _ := user.LookupId("1000")
fmt.Println(u.Username) // "hardik"

// Get current user
current, _ := user.Current()

// Check if running as root
if os.Geteuid() == 0 {
    // Running as root
}
```

## Common Permission Errors

### EACCES (Permission denied)
- File/directory permissions insufficient
- Check with `ls -la` and verify user context

### EPERM (Operation not permitted)
- Usually requires root/CAP_*
- Check if running with appropriate privileges

### Debugging Permission Issues
```bash
# Check file permissions
ls -la /path/to/file

# Check process user context
cat /proc/<pid>/status | grep -E "^(Uid|Gid):"

# Check capabilities
cat /proc/<pid>/status | grep Cap

# Trace syscalls for permission errors
strace -e trace=open,access,stat <command>
```

## Namespaces (Container Context)

```
/proc/<pid>/ns/ contains namespace references:
- mnt   (mount namespace)
- pid   (PID namespace)
- net   (network namespace)
- user  (user namespace)
- uts   (hostname namespace)
- ipc   (IPC namespace)
- cgroup (cgroup namespace)
```

Process in container has different namespace IDs than host processes.
