// pkg/docker/queries.go
package docker

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

type InstrumentationInfo struct {
	ContainerID    string
	ContainerName  string
	InstrumentedAt string
	AgentPath      string
	ServiceName    string
	OriginalConfig string
	OriginalEnv    map[string]string
}

func (do *DockerOperations) GetInstrumentedContainers() ([]types.Container, error) {
	listOptions := container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelInstrumented+"=true"),
		),
	}

	containers, err := do.cli.ContainerList(do.ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list instrumented containers: %w", err)
	}

	return containers, nil
}

func (do *DockerOperations) GetContainerInstrumentationInfo(containerID string) (*InstrumentationInfo, error) {
	containerInfo, err := do.cli.ContainerInspect(do.ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	if containerInfo.Config.Labels[LabelInstrumented] != "true" {
		return nil, fmt.Errorf("container is not instrumented")
	}

	info := &InstrumentationInfo{
		ContainerID:    containerInfo.ID,
		ContainerName:  strings.TrimPrefix(containerInfo.Name, "/"),
		InstrumentedAt: containerInfo.Config.Labels[LabelInstrumentedAt],
		AgentPath:      containerInfo.Config.Labels[LabelAgentPath],
		ServiceName:    containerInfo.Config.Labels[LabelServiceName],
		OriginalConfig: containerInfo.Config.Labels[LabelOriginalConfig],
	}

	// Deserialize original environment
	if originalEnvStr := containerInfo.Config.Labels[LabelOriginalEnv]; originalEnvStr != "" {
		json.Unmarshal([]byte(originalEnvStr), &info.OriginalEnv)
	}

	return info, nil
}
