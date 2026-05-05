package discovery

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ContainerInfo holds information about a process running in a container
type ContainerInfo struct {
	IsContainer   bool   `json:"is_container"`
	ContainerID   string `json:"container_id,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
	Runtime       string `json:"runtime,omitempty"` // docker, podman, containerd, etc.
}

type containerNameEntry struct {
	Name     string
	LastSeen int64
}

var (
	// Global cache specifically for ID -> Name resolution.
	// This survives across different execution cycles of the discovery agent.
	globalNameCache = make(map[string]containerNameEntry)
	globalNameMu    sync.RWMutex

	globalCacheHits   atomic.Int64
	globalCacheMisses atomic.Int64
)

func getCachedContainerName(id string) (string, bool) {
	globalNameMu.Lock() // Needs Write Lock to update LastSeen
	defer globalNameMu.Unlock()

	entry, exists := globalNameCache[id]
	if exists {
		// Update timestamp to keep it alive (LRU style)
		entry.LastSeen = time.Now().Unix()
		globalNameCache[id] = entry

		globalCacheHits.Add(1)
		return entry.Name, true
	}
	return "", false
}

func cacheContainerName(id, name string) {
	if id == "" || name == "" {
		return
	}
	globalNameMu.Lock()
	defer globalNameMu.Unlock()

	globalNameCache[id] = containerNameEntry{
		Name:     name,
		LastSeen: time.Now().Unix(),
	}
}

// PruneContainerNameCache removes container names not used in the last 20 minutes
func PruneContainerNameCache() {
	globalNameMu.Lock()
	defer globalNameMu.Unlock()

	// 20 minute TTL (adjust as needed)
	threshold := time.Now().Add(-20 * time.Minute).Unix()

	for id, entry := range globalNameCache {
		if entry.LastSeen < threshold {
			delete(globalNameCache, id)
		}
	}
}

func PrintContainerCacheStats() {
	hits := globalCacheHits.Load()
	misses := globalCacheMisses.Load()
	total := hits + misses

	if total == 0 {
		return
	}

	efficiency := float64(hits) / float64(total) * 100
	fmt.Printf("\n[Container Cache Stats] Hits: %v | Misses: %v | Efficiency: %v\n",
		hits, misses, efficiency)
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
	// If it's in a .scope but not init.scope or user.slice, it's likely a container.
	// user.slice scopes are regular desktop/terminal apps, not containers.
	if strings.Contains(content, ".scope") &&
		!strings.Contains(content, "init.scope") &&
		!strings.Contains(content, "user.slice") {
		return &ContainerInfo{
			IsContainer: true,
			Runtime:     "generic-container",
		}, nil
	}

	return &ContainerInfo{IsContainer: false}, nil
}

// ClearCache clears the internal container detection cache
func (cd *ContainerDetector) ClearCache() {
	cd.cacheMu.Lock()
	defer cd.cacheMu.Unlock()
	cd.containerCache = make(map[string]*ContainerInfo)
}
