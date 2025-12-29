package discovery

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ContainerInfo holds information about a process running in a container
type ContainerInfo struct {
	IsContainer   bool   `json:"is_container"`
	ContainerID   string `json:"container_id,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
	Runtime       string `json:"runtime,omitempty"` // docker, podman, containerd, etc.
}

// ContainerDetector provides methods to detect if a process is running in a container
type ContainerDetector struct {
	// Cache for container lookups to avoid repeated Docker calls
	containerCache map[string]*ContainerInfo
	cacheMu        sync.RWMutex
}

// NewContainerDetector creates a new container detector
func NewContainerDetector() *ContainerDetector {
	return &ContainerDetector{
		containerCache: make(map[string]*ContainerInfo),
	}
}

func (cd *ContainerDetector) IsProcessInContainer(pid int32) (*ContainerInfo, error) {
	// 1. QUICK CHECK: Every process has a Cgroup. Check that first.
	// This is a simple file read, much cheaper than Namespace or Exec checks.
	info, err := cd.checkCgroup(pid)
	if err != nil || !info.IsContainer {
		return info, err
	}

	// 2. SMART CACHE: Use ContainerID as the key, not the PID!
	if info.ContainerID != "" {
		cd.cacheMu.RLock()
		if cached, exists := cd.containerCache[info.ContainerID]; exists {
			cd.cacheMu.RUnlock()
			return cached, nil
		}
		cd.cacheMu.RUnlock()
	}

	// 3. DEFER NAME LOOKUP: Don't call 'docker inspect' here.
	// Wait until you are 100% sure this is a Java/Node process you want to keep.

	cd.cacheMu.Lock()
	cd.containerCache[info.ContainerID] = info
	cd.cacheMu.Unlock()

	return info, nil
}

// checkCgroup examines the process cgroup to detect container runtimes
func (cd *ContainerDetector) checkCgroup(pid int32) (*ContainerInfo, error) {
	cgroupPath := fmt.Sprintf("/proc/%d/cgroup", pid)
	data, err := os.ReadFile(cgroupPath)
	if err != nil {
		return &ContainerInfo{IsContainer: false}, err
	}

	content := string(data)
	if content == "" {
		return &ContainerInfo{IsContainer: false}, nil
	}

	// 1. DOCKER & CONTAINERD
	// v1: /docker/<ID> or /containerd/<ID>
	// v2: /system.slice/docker-<ID>.scope or /system.slice/containerd-<ID>.scope
	dockerRegex := regexp.MustCompile(`(?:docker-|containerd-|/docker/|/containerd/)([a-fA-F0-9]{64})`)
	if matches := dockerRegex.FindStringSubmatch(content); len(matches) > 1 {
		return &ContainerInfo{
			IsContainer: true,
			ContainerID: matches[1],
			Runtime:     "docker/containerd",
		}, nil
	}

	// 2. KUBERNETES (CRI-O / Containerd)
	// Matches: cri-containerd-<ID>.scope, crio-<ID>.scope, or kubepods paths
	k8sRegex := regexp.MustCompile(`(?:cri-containerd-|crio-|/kubepods.*/pod.*/)([a-fA-F0-9]{64})`)
	if matches := k8sRegex.FindStringSubmatch(content); len(matches) > 1 {
		return &ContainerInfo{
			IsContainer: true,
			ContainerID: matches[1],
			Runtime:     "kubernetes",
		}, nil
	}

	// 3. PODMAN
	// Matches: libpod-<ID>.scope or /libpod-<ID>
	podmanRegex := regexp.MustCompile(`(?:libpod-|/libpod-)([a-fA-F0-9]{64})`)
	if matches := podmanRegex.FindStringSubmatch(content); len(matches) > 1 {
		return &ContainerInfo{
			IsContainer: true,
			ContainerID: matches[1],
			Runtime:     "podman",
		}, nil
	}

	// 4. LXC
	lxcRegex := regexp.MustCompile(`/lxc/([^/\n]+)`)
	if matches := lxcRegex.FindStringSubmatch(content); len(matches) > 1 {
		return &ContainerInfo{
			IsContainer: true,
			ContainerID: matches[1],
			Runtime:     "lxc",
		}, nil
	}

	// 5. GENERIC FALLBACK (Systemd Scopes)
	// If it's in a .scope but not the init.scope or user.slice, it's likely a container
	if strings.Contains(content, ".scope") && !strings.Contains(content, "init.scope") {
		return &ContainerInfo{
			IsContainer: true,
			Runtime:     "generic-container",
		}, nil
	}

	return &ContainerInfo{IsContainer: false}, nil
}

func (cd *ContainerDetector) checkNamespaceFallback(pid int32, info *ContainerInfo) {
	if info.IsContainer {
		return // Already detected
	}

	hostNs, _ := os.Readlink("/proc/1/ns/mnt")
	procNs, _ := os.Readlink(fmt.Sprintf("/proc/%d/ns/mnt", pid))

	if hostNs != "" && procNs != "" && hostNs != procNs {
		info.IsContainer = true
		info.Runtime = "namespace-isolated" // Container detected via isolation
	}
}

// checkMountNamespace checks if the process is in a different mount namespace
func (cd *ContainerDetector) checkMountNamespace(pid int32) (*ContainerInfo, error) {
	// Read the mount namespace for the process
	procNsPath := fmt.Sprintf("/proc/%d/ns/mnt", pid)
	procNs, err := os.Readlink(procNsPath)
	if err != nil {
		return nil, err
	}

	// Compare with init process (PID 1) mount namespace
	initNsPath := "/proc/1/ns/mnt"
	initNs, err := os.Readlink(initNsPath)
	if err != nil {
		return nil, err
	}

	// If mount namespaces differ, likely in a container
	if procNs != initNs {
		// Additional check: read /proc/PID/mountinfo to look for container-specific mounts
		if cd.hasContainerMounts(pid) {
			return &ContainerInfo{
				IsContainer: true,
				Runtime:     "unknown",
			}, nil
		}
	}

	return &ContainerInfo{IsContainer: false}, nil
}

// hasContainerMounts checks for container-specific mount patterns
func (cd *ContainerDetector) hasContainerMounts(pid int32) bool {
	mountinfoPath := fmt.Sprintf("/proc/%d/mountinfo", pid)
	file, err := os.Open(mountinfoPath)
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Look for container-specific mount patterns
		containerMountPatterns := []string{
			"/var/lib/docker/",
			"/var/lib/containers/",
			"overlay",
			"docker/containers",
			"podman/",
		}

		for _, pattern := range containerMountPatterns {
			if strings.Contains(line, pattern) {
				return true
			}
		}
	}
	return false
}

// checkEnvironment looks for container-specific environment variables
func (cd *ContainerDetector) checkEnvironment(pid int32) (*ContainerInfo, error) {
	environPath := fmt.Sprintf("/proc/%d/environ", pid)
	data, err := os.ReadFile(environPath)
	if err != nil {
		return nil, err
	}

	// Environment variables are null-separated
	envVars := strings.Split(string(data), "\x00")

	for _, envVar := range envVars {
		// Docker environment indicators
		if strings.HasPrefix(envVar, "HOSTNAME=") && len(strings.TrimPrefix(envVar, "HOSTNAME=")) == 12 {
			// Docker containers often have 12-character hostnames
			return &ContainerInfo{
				IsContainer: true,
				Runtime:     "docker",
			}, nil
		}

		// Container-specific environment variables
		containerEnvPatterns := []string{
			"container=",
			"DOCKER_CONTAINER=",
			"PODMAN_CONTAINER=",
			"KUBERNETES_SERVICE_HOST=",
			"K8S_",
		}

		for _, pattern := range containerEnvPatterns {
			if strings.HasPrefix(envVar, pattern) {
				return &ContainerInfo{
					IsContainer: true,
					Runtime:     "unknown",
				}, nil
			}
		}
	}

	return &ContainerInfo{IsContainer: false}, nil
}

// checkParentProcess examines the parent process chain for container runtimes
func (cd *ContainerDetector) checkParentProcess(pid int32) (*ContainerInfo, error) {
	currentPID := pid

	// Walk up the process tree (max 10 levels to avoid infinite loops)
	for i := 0; i < 10; i++ {
		statPath := fmt.Sprintf("/proc/%d/stat", currentPID)
		data, err := os.ReadFile(statPath)
		if err != nil {
			break
		}

		// Parse stat file to get command and parent PID
		fields := strings.Fields(string(data))
		if len(fields) < 4 {
			break
		}

		comm := strings.Trim(fields[1], "()")

		// Check if parent is a container runtime
		containerRuntimes := []string{
			"dockerd",
			"docker-proxy",
			"containerd",
			"containerd-shim",
			"runc",
			"conmon",
			"podman",
		}

		for _, runtime := range containerRuntimes {
			if strings.Contains(comm, runtime) {
				return &ContainerInfo{
					IsContainer: true,
					Runtime:     runtime,
				}, nil
			}
		}

		// Get parent PID for next iteration
		ppid, err := strconv.Atoi(fields[3])
		if err != nil || ppid <= 1 {
			break
		}
		currentPID = int32(ppid)
	}

	return &ContainerInfo{IsContainer: false}, nil
}

// Helper functions to extract container IDs from cgroup paths

func (cd *ContainerDetector) extractDockerIDFromCgroup(cgroupLine string) string {
	// Docker cgroup patterns:
	// /docker/abcd1234567890abcd1234567890abcd1234567890abcd1234567890abcd1234
	// /system.slice/docker-abcd1234567890.scope

	dockerIDRegex := regexp.MustCompile(`/docker/([a-f0-9]{64})`)
	if matches := dockerIDRegex.FindStringSubmatch(cgroupLine); len(matches) > 1 {
		return matches[1][:12] // Return short ID
	}

	dockerScopeRegex := regexp.MustCompile(`docker-([a-f0-9]{64})\.scope`)
	if matches := dockerScopeRegex.FindStringSubmatch(cgroupLine); len(matches) > 1 {
		return matches[1][:12] // Return short ID
	}

	return ""
}

func (cd *ContainerDetector) extractPodmanIDFromCgroup(cgroupLine string) string {
	// Podman cgroup patterns:
	// /machine.slice/libpod-abcd1234567890.scope
	// /libpod-abcd1234567890abcd1234567890abcd1234567890abcd1234567890abcd1234.scope

	podmanRegex := regexp.MustCompile(`libpod-([a-f0-9]+)`)
	if matches := podmanRegex.FindStringSubmatch(cgroupLine); len(matches) > 1 {
		id := matches[1]
		if len(id) >= 12 {
			return id[:12] // Return short ID
		}
		return id
	}

	return ""
}

func (cd *ContainerDetector) extractContainerdIDFromCgroup(cgroupLine string) string {
	// Containerd cgroup patterns:
	// /containerd/abcd1234567890abcd1234567890abcd1234567890abcd1234567890abcd1234

	containerdRegex := regexp.MustCompile(`/containerd/([a-f0-9]{64})`)
	if matches := containerdRegex.FindStringSubmatch(cgroupLine); len(matches) > 1 {
		return matches[1][:12] // Return short ID
	}

	return ""
}

func (cd *ContainerDetector) extractLXCIDFromCgroup(cgroupLine string) string {
	// LXC cgroup patterns:
	// /lxc/container-name

	lxcRegex := regexp.MustCompile(`/lxc/([^/]+)`)
	if matches := lxcRegex.FindStringSubmatch(cgroupLine); len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// GetContainerNameByID attempts to get the container name from its ID
func (cd *ContainerDetector) GetContainerNameByID(containerID, runtime string) string {
	if containerID == "" {
		return ""
	}

	switch runtime {
	case "docker":
		return cd.getDockerContainerName(containerID)
	case "docker/containerd":
		return cd.getDockerContainerName(containerID)
	case "podman":
		return cd.getPodmanContainerName(containerID)
	default:
		return ""
	}
}

// getDockerContainerName gets Docker container name by ID
func (cd *ContainerDetector) getDockerContainerName(containerID string) string {
	// Try to get container name using docker inspect
	cmd := exec.Command("docker", "inspect", "--format={{.Name}}", containerID)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	name := strings.TrimSpace(string(output))
	// Docker container names start with '/', remove it
	return strings.TrimPrefix(name, "/")
}

func (cd *ContainerDetector) getPodmanContainerName(containerID string) string {
	// Try to get container name using podman inspect
	cmd := exec.Command("podman", "inspect", "--format={{.Name}}", containerID)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

// ClearCache clears the internal container detection cache
func (cd *ContainerDetector) ClearCache() {
	cd.cacheMu.Lock()
	defer cd.cacheMu.Unlock()
	cd.containerCache = make(map[string]*ContainerInfo)
}
