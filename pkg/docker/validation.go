// pkg/docker/validation.go
package docker

import (
	"strings"
)

type ValidationResult struct {
	ContainerID   string
	Issues        []string
	Warnings      []string
	CanBeRepaired bool
}

func (do *DockerOperations) ValidateInstrumentedContainer(containerID string) (*ValidationResult, error) {
	result := &ValidationResult{
		ContainerID: containerID,
		Issues:      []string{},
		Warnings:    []string{},
	}

	containerInfo, err := do.cli.ContainerInspect(do.ctx, containerID)
	if err != nil {
		result.Issues = append(result.Issues, "Container not found")
		return result, nil
	}

	// Validate instrumentation labels
	if containerInfo.Config.Labels[LabelInstrumented] != "true" {
		result.Issues = append(result.Issues, "Container not marked as instrumented")
		return result, nil
	}

	// Validate agent mount exists
	agentMountExists := false
	for _, bind := range containerInfo.HostConfig.Binds {
		if strings.Contains(bind, DefaultContainerAgentPath) {
			agentMountExists = true
			break
		}
	}

	if !agentMountExists {
		result.Issues = append(result.Issues, "Agent volume mount missing")
		result.CanBeRepaired = true
	}

	return result, nil
}
