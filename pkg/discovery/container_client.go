package discovery

import "context"

// ContainerClient abstracts container runtime APIs for metadata retrieval.
// Implementations use HTTP over Unix sockets (Docker/Podman API) to avoid
// shelling out to CLI tools.
type ContainerClient interface {
	Name() string
	Available(ctx context.Context) bool
	InspectBatch(ctx context.Context, ids []string) (map[string]ContainerMeta, error)
}

type ContainerMeta struct {
	ID          string
	Name        string
	Image       string
	ImageTag    string
	Labels      map[string]string
	ComposeInfo *ComposeInfo
}

type ComposeInfo struct {
	Project string
	Service string
	WorkDir string
}

// initContainerClients probes available runtimes and returns ready clients.
func initContainerClients(ctx context.Context) []ContainerClient {
	candidates := []ContainerClient{
		newDockerClient(),
		newPodmanClient(),
	}
	var ready []ContainerClient
	for _, c := range candidates {
		if c.Available(ctx) {
			ready = append(ready, c)
		}
	}
	return ready
}

// batchResolveContainerNames resolves container names for all processes that
// are in containers but don't yet have a name. It groups by runtime and calls
// InspectBatch once per runtime to minimize API calls.
func batchResolveContainerNames(ctx context.Context, processes []*Process, clients []ContainerClient) {
	if len(clients) == 0 {
		return
	}

	// Build lookup: runtime -> set of container IDs needing resolution
	type resolveRequest struct {
		containerID string
		runtime     string
	}
	byRuntime := make(map[string][]string)
	for _, proc := range processes {
		if proc.ContainerInfo == nil || !proc.ContainerInfo.IsContainer {
			continue
		}
		if proc.ContainerInfo.ContainerID == "" || proc.ContainerInfo.ContainerName != "" {
			continue
		}
		// Check global cache first
		if name, hit := getCachedContainerName(proc.ContainerInfo.ContainerID); hit {
			proc.ContainerInfo.ContainerName = name
			continue
		}
		byRuntime[proc.ContainerInfo.Runtime] = append(byRuntime[proc.ContainerInfo.Runtime], proc.ContainerInfo.ContainerID)
	}

	// Deduplicate IDs per runtime
	for rt, ids := range byRuntime {
		seen := make(map[string]struct{}, len(ids))
		deduped := ids[:0]
		for _, id := range ids {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				deduped = append(deduped, id)
			}
		}
		byRuntime[rt] = deduped
	}

	// Resolve via matching client
	resolved := make(map[string]ContainerMeta)
	for _, client := range clients {
		for rt, ids := range byRuntime {
			if !runtimeMatchesClient(rt, client.Name()) {
				continue
			}
			metas, err := client.InspectBatch(ctx, ids)
			if err != nil {
				continue
			}
			for id, meta := range metas {
				resolved[id] = meta
				cacheContainerName(id, meta.Name)
			}
		}
	}

	// Apply resolved names back to processes
	for _, proc := range processes {
		if proc.ContainerInfo == nil || proc.ContainerInfo.ContainerName != "" {
			continue
		}
		if meta, ok := resolved[proc.ContainerInfo.ContainerID]; ok {
			proc.ContainerInfo.ContainerName = meta.Name
		}
	}
}

// applyContainerServiceNames updates ServiceName for container processes
// whose container name was resolved after the initial extractServiceName ran
// (which didn't have the name yet). Container name is the highest-priority
// service name source.
func applyContainerServiceNames(processes []*Process) {
	for _, proc := range processes {
		if proc.ContainerInfo == nil || !proc.ContainerInfo.IsContainer {
			continue
		}
		if proc.ContainerInfo.ContainerName == "" {
			continue
		}
		// Container name is highest priority — always override
		proc.ServiceName = proc.ContainerInfo.ContainerName
	}
}

func runtimeMatchesClient(runtime, clientName string) bool {
	switch clientName {
	case "docker":
		return runtime == "docker" || runtime == "docker/containerd"
	case "podman":
		return runtime == "podman"
	}
	return false
}
