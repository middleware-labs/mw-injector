package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/k0kubun/pp"
	"github.com/middleware-labs/java-injector/pkg/config"
	"github.com/middleware-labs/java-injector/pkg/discovery"
	"gopkg.in/yaml.v3"
)

const (
	// DefaultAgentPath is the default path to mount the agent in containers
	DefaultContainerAgentPath = "/opt/middleware/agents/middleware-javaagent.jar"

	// StateFile stores instrumented container information
	StateFile = "/etc/middleware/docker/instrumented.json"
)

// DockerOperations handles Docker container instrumentation operations
type DockerOperations struct {
	ctx           context.Context
	discoverer    *discovery.DockerDiscoverer
	hostAgentPath string // TODO: Make this name better. Its not exactly a hostAgent
	cli           *client.Client
}

// NewDockerOperations creates a new Docker operations handler
func NewDockerOperations(ctx context.Context, hostAgentPath string) (*DockerOperations, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("could not create a client for docker operations %v", err.Error())
	}
	return &DockerOperations{
		ctx:           ctx,
		discoverer:    discovery.NewDockerDiscoverer(ctx),
		hostAgentPath: hostAgentPath,
		cli:           cli,
	}, nil
}

// InstrumentedState represents the state of instrumented containers
type InstrumentedState struct {
	Containers map[string]ContainerState `json:"containers"`
	UpdatedAt  time.Time                 `json:"updated_at"`
}

type ComposeFile struct {
	Version  string             `yaml:"version,omitempty"`
	Services map[string]Service `yaml:"services"`
	Networks map[string]Network `yaml:"networks,omitempty"`
	Volumes  map[string]Volume  `yaml:"volumes,omitempty"`
}

// Service represents a service in docker-compose
type Service struct {
	Build       interface{} `yaml:"build,omitempty"`
	Image       string      `yaml:"image,omitempty"`
	Ports       []string    `yaml:"ports,omitempty"`
	Environment []string    `yaml:"environment,omitempty"`
	Volumes     []string    `yaml:"volumes,omitempty"`
	Networks    []string    `yaml:"networks,omitempty"`
	Restart     string      `yaml:"restart,omitempty"`
	DependsOn   []string    `yaml:"depends_on,omitempty"`
	Command     interface{} `yaml:"command,omitempty"`
	Entrypoint  interface{} `yaml:"entrypoint,omitempty"`
	WorkingDir  string      `yaml:"working_dir,omitempty"`
	User        string      `yaml:"user,omitempty"`

	// Keep raw YAML for fields we don't explicitly handle
	Extra map[string]interface{} `yaml:",inline"`
}

// Network represents a network definition
type Network struct {
	Driver string                 `yaml:"driver,omitempty"`
	Extra  map[string]interface{} `yaml:",inline"`
}

// Volume represents a volume definition
type Volume struct {
	Driver string                 `yaml:"driver,omitempty"`
	Extra  map[string]interface{} `yaml:",inline"`
}

// ComposeModifier handles Docker Compose file modifications
type ComposeModifier struct {
	filePath string
}

// NewComposeModifier creates a new compose file modifier
func NewComposeModifier(filePath string) *ComposeModifier {
	return &ComposeModifier{
		filePath: filePath,
	}
}

// ContainerState stores information about an instrumented container
type ContainerState struct {
	ContainerID    string            `json:"container_id"`
	ContainerName  string            `json:"container_name"`
	ImageName      string            `json:"image_name"`
	InstrumentedAt time.Time         `json:"instrumented_at"`
	AgentPath      string            `json:"agent_path"`
	OriginalEnv    map[string]string `json:"original_env"`
	ComposeFile    string            `json:"compose_file,omitempty"`
	ComposeService string            `json:"compose_service,omitempty"`

	RecreationCommand string `json:"recreation_command,omitempty"`
	OriginalConfig    string `json:"original_config,omitempty"`
}

// InstrumentContainer instruments a specific Docker container
func (do *DockerOperations) InstrumentContainer(containerName string, cfg *config.ProcessConfiguration) error {
	// Discover the container
	container, err := do.discoverer.GetContainerByName(containerName)
	if err != nil {
		return fmt.Errorf("container not found: %w", err)
	}

	// Check if already instrumented
	if container.Instrumented {
		return fmt.Errorf("container %s is already instrumented", containerName)
	}

	// Determine instrumentation strategy
	if container.IsCompose {
		return do.instrumentComposeContainer(container, cfg)
	}

	return do.instrumentStandaloneContainer(container, cfg)
}

// instrumentStandaloneContainer instruments a standalone Docker container
func (do *DockerOperations) instrumentStandaloneContainer(container *discovery.DockerContainer, cfg *config.ProcessConfiguration) error {
	fmt.Printf("🔧 Instrumenting standalone container: %s\n", container.ContainerName)

	// Step 1: Get original container configuration
	containerConfig, err := do.getContainerConfig(container.ContainerID)
	if err != nil {
		return fmt.Errorf("failed to get container config: %w", err)
	}

	// Step 2: Build new environment variables with instrumentation
	newEnv := do.buildInstrumentationEnv(container, cfg)

	// Convert map to slice format
	envSlice := make([]string, 0, len(newEnv))
	for k, v := range newEnv {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	// Step 3: Recreate container with volume mount + new environment
	if err := do.recreateContainerWithAPI(*containerConfig, envSlice); err != nil {
		return fmt.Errorf("failed to recreate container: %w", err)
	}

	// Step 4: Save state
	if err := do.saveContainerState(container, cfg); err != nil {
		fmt.Printf("   ⚠️  Warning: Could not save state: %v\n", err)
	}

	fmt.Printf("   ✅ Container %s instrumented successfully\n", container.ContainerName)
	return nil
}

// buildInstrumentedDockerRunCommand creates docker run command with instrumentation
func (do *DockerOperations) buildInstrumentedDockerRunCommand(config container.InspectResponse, env map[string]string, containerName, imageName string) string {
	pp.Println("BUILDING INSTRUMENTED DOCKER RUN COMMAND")
	var cmdParts []string
	cmdParts = append(cmdParts, "docker", "run", "-d")
	cmdParts = append(cmdParts, "--name", containerName)

	configSection := config.Config

	// Add environment variables (with instrumentation)
	for k, v := range env {
		cmdParts = append(cmdParts, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// Add original volume mounts
	if len(config.Mounts) > 0 {
		for _, m := range config.Mounts {
			if m.Source != "" && m.Destination != "" {
				mode := "rw"
				if !m.RW {
					mode = "ro"
				}
				cmdParts = append(cmdParts, "-v", fmt.Sprintf("%s:%s:%s", m.Source, m.Destination, mode))
			}
		}
	}

	// Add agent volume mount
	cmdParts = append(cmdParts, "-v", fmt.Sprintf("%s:%s:ro", do.hostAgentPath, DefaultContainerAgentPath))

	// Add port mappings
	if networkSettings := config.NetworkSettings; networkSettings != nil {
		if ports := networkSettings.Ports; ports != nil {
			for containerPort, bindings := range ports {
				if len(bindings) > 0 {
					binding := bindings[0]
					if binding.HostPort != "" {
						hostIP := "0.0.0.0"
						if binding.HostIP != "" {
							hostIP = binding.HostIP
						}
						cmdParts = append(cmdParts, "-p", fmt.Sprintf("%s:%s:%s", hostIP, binding.HostPort, containerPort))
					}
				}
			}
		}
	}

	// Add networks
	if config.NetworkSettings != nil {
		if networks := config.NetworkSettings.Networks; networks != nil {
			for networkName := range config.NetworkSettings.Networks {
				if networkName != "bridge" {
					cmdParts = append(cmdParts, "--network", networkName)
				}
			}
		}
	}

	// Add restart policy

	if hostConfig := config.HostConfig; hostConfig != nil {
		if name := hostConfig.RestartPolicy.Name; name != "" && name != "no" {
			if maxRetries := hostConfig.RestartPolicy.MaximumRetryCount; maxRetries > 0 {
				cmdParts = append(cmdParts, "--restart", fmt.Sprintf("%s:%d", name, int(maxRetries)))
			} else {
				cmdParts = append(cmdParts, "--restart", string(name))
			}
		}
	}

	if workingDir := configSection.WorkingDir; workingDir != "" {
		cmdParts = append(cmdParts, "--workdir", workingDir)
	}

	if user := configSection.User; user != "" {
		cmdParts = append(cmdParts, "--user", user)
	}

	// Add image
	if image := configSection.Image; image != "" {
		cmdParts = append(cmdParts, image)
	}

	if cmd := configSection.Cmd; len(cmd) > 0 {
		for _, c := range cmd {
			cmdParts = append(cmdParts, c)
		}
	}

	pp.Println("ORIGINAL COMMAND: ", strings.Join(cmdParts, " "))

	return strings.Join(cmdParts, " ")
}

// Updated instrumentComposeContainer method to use the new YAML modifier
func (do *DockerOperations) instrumentComposeContainer(container *discovery.DockerContainer, cfg *config.ProcessConfiguration) error {
	fmt.Printf("🔧 Instrumenting Docker Compose container: %s\n", container.ContainerName)

	if container.ComposeFile == "" {
		return fmt.Errorf("compose file not found for container %s", container.ContainerName)
	}

	modifier := NewComposeModifier(container.ComposeFile)

	// Step 1: Validate compose file
	if err := modifier.ValidateComposeFile(); err != nil {
		return fmt.Errorf("invalid compose file: %w", err)
	}

	// Step 2: Create backup
	backupPath, err := modifier.BackupComposeFile()
	if err != nil {
		fmt.Printf("   ⚠️  Warning: Could not backup compose file: %v\n", err)
		backupPath = "" // Continue without backup
	} else {
		fmt.Printf("   ✅ Backup created: %s\n", filepath.Base(backupPath))
	}

	// Step 3: Modify compose file
	if err := do.modifyComposeFile(container, cfg); err != nil {
		// Restore backup on failure if we have one
		if backupPath != "" {
			modifier.RestoreFromBackup(backupPath)
		}
		return fmt.Errorf("failed to modify compose file: %w", err)
	}

	// Step 4: Validate modified file
	if err := modifier.ValidateComposeFile(); err != nil {
		// Restore backup on validation failure
		if backupPath != "" {
			modifier.RestoreFromBackup(backupPath)
		}
		return fmt.Errorf("modified compose file is invalid: %w", err)
	}

	// Step 5: Recreate service using docker-compose
	fmt.Println("   🔄 Recreating service...")
	if err := do.recreateComposeService(container); err != nil {
		// Restore backup on failure
		if backupPath != "" {
			modifier.RestoreFromBackup(backupPath)
			fmt.Println("   🔙 Restored original compose file due to recreation failure")
		}
		return fmt.Errorf("failed to recreate service: %w", err)
	}

	// Step 6: Verify instrumentation worked
	if err := do.verifyContainerInstrumentation(container.ContainerName); err != nil {
		fmt.Printf("   ⚠️  Warning: Instrumentation verification failed: %v\n", err)
		fmt.Println("   🔍 Check container logs for issues")
	} else {
		fmt.Println("   ✅ Instrumentation verified")
	}

	// Step 7: Save state
	if err := do.saveContainerState(container, cfg); err != nil {
		fmt.Printf("   ⚠️  Warning: Could not save state: %v\n", err)
	}

	fmt.Printf("   ✅ Container %s instrumented successfully\n", container.ContainerName)
	return nil
}

// buildOriginalDockerRunCommand creates the original docker run command before instrumentation
func (do *DockerOperations) buildOriginalDockerRunCommand(config *container.InspectResponse, containerName string) string {
	var cmdParts []string
	cmdParts = append(cmdParts, "docker", "run", "-d")
	cmdParts = append(cmdParts, "--name", containerName)

	configSection := config.Config

	// Add original environment variables (without instrumentation)
	for _, e := range configSection.Env {
		if !strings.HasPrefix(e, "MW_") &&
			!strings.HasPrefix(e, "OTEL_") &&
			!(strings.HasPrefix(e, "JAVA_TOOL_OPTIONS=") && strings.Contains(e, "javaagent")) {

			cmdParts = append(cmdParts, "-e", e)
		}
	}

	// Add original volume mounts (excluding our agent mount)
	for _, mount := range config.Mounts {
		src := mount.Source
		dst := mount.Destination

		if src != "" && dst != "" {
			if dst == DefaultContainerAgentPath {
				continue
			}
			mode := "rw"
			if !mount.RW {
				mode = "ro"
			}
			cmdParts = append(cmdParts, "-v", fmt.Sprintf("%s:%s:%s", src, dst, mode))
		}
	}

	// Add port mappings
	if config.NetworkSettings != nil {
		for containerPort, bindings := range config.NetworkSettings.NetworkSettingsBase.Ports {
			if len(bindings) > 0 {
				binding := bindings[0]
				if binding.HostPort != "" {
					hostIP := "0.0.0.0"
					if binding.HostIP != "" {
						hostIP = binding.HostIP
					}
					cmdParts = append(cmdParts, "-p", fmt.Sprintf("%s:%s:%s", hostIP, binding.HostPort, containerPort))
				}
			}
		}
	}

	// Add networks
	if config.NetworkSettings != nil {
		if config.NetworkSettings.Networks != nil {
			for networkName := range config.NetworkSettings.Networks {
				if networkName != "bridge" {
					cmdParts = append(cmdParts, "--network", networkName)
				}
			}
		}
	}

	// Add restart policy
	if hostConfig := config.HostConfig; hostConfig != nil {
		if name := hostConfig.RestartPolicy.Name; name != "" && name != "no" {
			if maxRetries := hostConfig.RestartPolicy.MaximumRetryCount; maxRetries > 0 {
				cmdParts = append(cmdParts, "--restart", fmt.Sprintf("%s:%d", name, int(maxRetries)))
			} else {
				cmdParts = append(cmdParts, "--restart", string(name))
			}
		}

	}

	if workingDir := configSection.WorkingDir; workingDir != "" {
		cmdParts = append(cmdParts, "--workdir", workingDir)
	}

	if user := configSection.User; user != "" {
		cmdParts = append(cmdParts, "--user", user)
	}

	// Add original image
	if image := configSection.Image; image != "" {
		cmdParts = append(cmdParts, image)
	}

	// Add original command
	if cmd := configSection.Cmd; len(cmd) > 0 {
		for _, c := range cmd {
			cmdParts = append(cmdParts, c)
		}
	}
	pp.Println("ORIGINAL COMMAND: ", strings.Join(cmdParts, " "))
	return strings.Join(cmdParts, " ")
}

// saveContainerStateWithCommand saves container state with recreation command
func (do *DockerOperations) saveContainerStateWithCommand(container *discovery.DockerContainer, cfg *config.ProcessConfiguration, recreationCommand, originalConfig string) error {
	state, _ := do.loadState()
	if state.Containers == nil {
		state.Containers = make(map[string]ContainerState)
	}

	state.Containers[container.ContainerName] = ContainerState{
		ContainerID:       container.ContainerID,
		ContainerName:     container.ContainerName,
		ImageName:         container.ImageName,
		InstrumentedAt:    time.Now(),
		AgentPath:         do.hostAgentPath,
		OriginalEnv:       container.Environment,
		ComposeFile:       container.ComposeFile,
		ComposeService:    container.ComposeService,
		RecreationCommand: recreationCommand,
		OriginalConfig:    originalConfig, // Full original config for debugging
	}
	state.UpdatedAt = time.Now()

	return do.saveState(state)
}

// UninstrumentContainer removes instrumentation from a container
func (do *DockerOperations) UninstrumentContainer(containerName string) error {
	// Load state to check if container was instrumented by us
	state, err := do.loadState()
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	containerState, exists := state.Containers[containerName]
	if !exists {
		return fmt.Errorf("container %s was not instrumented by this tool", containerName)
	}

	fmt.Printf("🔧 Uninstrumenting container: %s\n", containerName)

	// Check if it's a compose container
	if containerState.ComposeFile != "" {
		return do.uninstrumentComposeContainer(&containerState)
	}

	return do.uninstrumentStandaloneContainer(&containerState)
}

// uninstrumentStandaloneContainer removes instrumentation from standalone container
func (do *DockerOperations) uninstrumentStandaloneContainer(state *ContainerState) error {
	// Check if we have the original recreation command
	if state.RecreationCommand == "" {
		fmt.Println("   ⚠️  Cannot fully restore container without original configuration")
		fmt.Println("   💡 Suggestion: Remove JAVA_TOOL_OPTIONS and MW_* env vars manually and restart")
		return do.removeContainerState(state.ContainerName)
	}

	fmt.Println("   🔄 Restoring original container configuration...")

	// Stop current container
	if err := do.stopContainerByName(state.ContainerName); err != nil {
		fmt.Printf("   ⚠️  Warning: Could not stop container: %v\n", err)
	}

	// Remove current container
	if err := do.removeContainerByName(state.ContainerName); err != nil {
		fmt.Printf("   ⚠️  Warning: Could not remove container: %v\n", err)
	}

	// Recreate with original command
	fmt.Printf("   Executing: %s\n", state.RecreationCommand)
	if err := do.runContainer(state.RecreationCommand); err != nil {
		return fmt.Errorf("failed to recreate container with original config: %w", err)
	}

	fmt.Printf("   ✅ Container %s restored to original configuration\n", state.ContainerName)

	// Remove from state
	return do.removeContainerState(state.ContainerName)
}

// uninstrumentComposeContainer removes instrumentation from compose container
func (do *DockerOperations) uninstrumentComposeContainer(state *ContainerState) error {
	// Restore backup compose file
	backupFile := state.ComposeFile + ".backup"
	if _, err := os.Stat(backupFile); err == nil {
		if err := do.copyFile(backupFile, state.ComposeFile); err != nil {
			return fmt.Errorf("failed to restore compose file: %w", err)
		}
		fmt.Println("   ✅ Compose file restored")

		// Recreate service
		fmt.Println("   🔄 Recreating service...")

		// Get container to recreate
		container, err := do.discoverer.GetContainerByName(state.ContainerName)
		if err == nil {
			do.recreateComposeService(container)
		}
	} else {
		fmt.Println("   ⚠️  Backup compose file not found")
		fmt.Println("   💡 Suggestion: Manually remove MW instrumentation from compose file and run 'docker-compose up -d'")
	}

	// Remove from state
	return do.removeContainerState(state.ContainerName)
}

// copyAgentToContainer copies the agent JAR to a running container
func (do *DockerOperations) copyAgentToContainer(containerID string) error {
	agentFileContent, err := os.ReadFile(do.hostAgentPath)
	if err != nil {
		return fmt.Errorf("failed to read agent file: %w", err)
	}
	tarBuffer, err := do.createAgentTar(agentFileContent)
	if err != nil {
		return fmt.Errorf("failed to create Agent tar: %w", err)
	}

	err = do.cli.CopyToContainer(
		do.ctx,
		containerID,
		"/opt/middleware/agents/",
		tarBuffer,
		container.CopyToContainerOptions{
			AllowOverwriteDirWithFile: false,
		},
	)

	if err != nil {
		return fmt.Errorf("failed to copy agent to container: %w", err)
	}

	fmt.Println("   ✅ Agent copied to container via Docker API")
	return nil
}

func (do *DockerOperations) createAgentTar(agentContent []byte) (*bytes.Buffer, error) {
	tarBuffer := &bytes.Buffer{}
	tarWriter := tar.NewWriter(tarBuffer)
	defer tarWriter.Close()

	// Create tar header for the agent file
	header := &tar.Header{
		Name:     "middleware-javaagent.jar",
		Size:     int64(len(agentContent)),
		Mode:     0644,
		ModTime:  time.Now(),
		Typeflag: tar.TypeReg,
	}

	if err := tarWriter.WriteHeader(header); err != nil {
		return nil, err
	}

	if _, err := tarWriter.Write(agentContent); err != nil {
		return nil, err
	}

	return tarBuffer, nil
}

func (do *DockerOperations) buildInstrumentationEnv(container *discovery.DockerContainer, cfg *config.ProcessConfiguration) map[string]string {
	env := make(map[string]string)

	// Copy existing environment
	for k, v := range container.Environment {
		env[k] = v
	}

	// Add JAVA_TOOL_OPTIONS
	javaToolOptions := fmt.Sprintf("-javaagent:%s", DefaultContainerAgentPath)
	if existing, ok := env["JAVA_TOOL_OPTIONS"]; ok {
		// Append to existing
		env["JAVA_TOOL_OPTIONS"] = existing + " " + javaToolOptions
	} else {
		env["JAVA_TOOL_OPTIONS"] = javaToolOptions
	}

	// Add MW configuration
	mwEnv := cfg.ToEnvironmentVariables()
	for k, v := range mwEnv {
		env[k] = v
	}

	// Set service name if not already set
	if env["MW_SERVICE_NAME"] == "" {
		env["MW_SERVICE_NAME"] = container.GetServiceName()
	}

	return env
}

// getContainerConfig gets full container configuration
func (do *DockerOperations) getContainerConfig(containerID string) (*container.InspectResponse, error) {
	ctx := context.Background()
	containerInfo, err := do.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		log.Fatal(err)
	}

	return &containerInfo, nil
}

// stopContainer stops a running container
func (do *DockerOperations) stopContainer(containerID string) error {
	// Docker uses a graceful shutdown process when stopping containers:
	// 1. First, Docker sends SIGTERM to the main process (PID 1) in the container
	// 2. The application has 'timeout' seconds to handle SIGTERM and shut down gracefully
	//    - This allows the app to: close database connections, save state, cleanup resources, etc.
	// 3. If the container is still running after the timeout expires, Docker sends SIGKILL
	//    - SIGKILL cannot be caught or ignored - it immediately terminates the process
	//    - This prevents containers from hanging indefinitely during shutdown
	//
	// 30 seconds is a reasonable timeout for most Java applications to:
	// - Complete current requests
	// - Close connection pools
	// - Flush logs and caches
	// - Perform other cleanup operations

	timeout := 30

	err := do.cli.ContainerStop(
		do.ctx,
		containerID,
		container.StopOptions{
			Timeout: &timeout,
		},
	)

	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", containerID, err)
	}

	return nil
}

// stopContainerByName stops a container by name
func (do *DockerOperations) stopContainerByName(name string) error {
	err := do.stopContainer(name)
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", name, err)
	}

	return nil
}

// removeContainer removes a container
func (do *DockerOperations) removeContainer(containerID string) error {
	err := do.cli.ContainerRemove(
		do.ctx,
		containerID,
		container.RemoveOptions{
			Force: true,
		},
	)

	if err != nil {
		return fmt.Errorf("failed to remove container %s: %w", containerID, err)
	}

	return nil
}

func (do *DockerOperations) updateContainerEnvironment(
	containerID string,
	newEnv []string,
) error {
	containerInfo, err := do.cli.ContainerInspect(do.ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	// For now, we'll still need to recreate the container because Docker doesn't
	// allow updating environment variables of existing containers
	// But we'll do it through the API instead of shell commands
	return do.recreateContainerWithAPI(containerInfo, newEnv)
}

func (do *DockerOperations) recreateContainerWithAPI(
	containerInfo container.InspectResponse,
	newEnv []string,
) error {
	config := &container.Config{
		Image:        containerInfo.Config.Image,
		Env:          newEnv,
		Cmd:          containerInfo.Config.Cmd,
		Entrypoint:   containerInfo.Config.Entrypoint,
		WorkingDir:   containerInfo.Config.WorkingDir,
		User:         containerInfo.Config.User,
		Labels:       containerInfo.Config.Labels,
		ExposedPorts: containerInfo.Config.ExposedPorts,
	}

	// Create host config based on existing one
	hostConfig := &container.HostConfig{
		Binds:         containerInfo.HostConfig.Binds,
		PortBindings:  containerInfo.HostConfig.PortBindings,
		RestartPolicy: containerInfo.HostConfig.RestartPolicy,
		NetworkMode:   containerInfo.HostConfig.NetworkMode,
		VolumeDriver:  containerInfo.HostConfig.VolumeDriver,
		VolumesFrom:   containerInfo.HostConfig.VolumesFrom,
		Resources:     containerInfo.HostConfig.Resources,
	}

	// 🔧 ADD AGENT VOLUME MOUNT HERE:
	agentMount := fmt.Sprintf("%s:%s:ro", do.hostAgentPath, DefaultContainerAgentPath)
	hostConfig.Binds = append(hostConfig.Binds, agentMount)

	// Network config
	networkConfig := &network.NetworkingConfig{
		EndpointsConfig: containerInfo.NetworkSettings.Networks,
	}

	containerName := containerInfo.Name
	if strings.HasPrefix(containerName, "/") {
		containerName = containerName[1:]
	}

	// Stop and remove old container
	if err := do.stopContainer(containerInfo.ID); err != nil {
		return fmt.Errorf("failed to stop old container: %w", err)
	}

	if err := do.removeContainer(containerInfo.ID); err != nil {
		return fmt.Errorf("failed to remove old container: %w", err)
	}

	// Create new container
	resp, err := do.cli.ContainerCreate(
		do.ctx,
		config,
		hostConfig,
		networkConfig,
		nil,
		containerName,
	)
	if err != nil {
		return fmt.Errorf("failed to create new container: %w", err)
	}

	// Start the new container
	if err := do.cli.ContainerStart(do.ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start new container: %w", err)
	}

	fmt.Printf("   ✅ Container recreated with ID: %s\n", resp.ID[:12])
	return nil
}

// removeContainerByName removes a container by name
func (do *DockerOperations) removeContainerByName(name string) error {
	cmd := exec.CommandContext(do.ctx, "docker", "rm", name)
	return cmd.Run()
}

// commitContainer commits a container to a new image
func (do *DockerOperations) commitContainer(containerID, imageName string) error {
	cmd := exec.CommandContext(do.ctx, "docker", "commit", containerID, imageName)
	return cmd.Run()
}

// buildDockerRunCommand builds a docker run command from container config
func (do *DockerOperations) buildDockerRunCommand(config map[string]interface{}, env map[string]string, containerName string) string {
	var cmdParts []string
	cmdParts = append(cmdParts, "docker", "run", "-d")
	cmdParts = append(cmdParts, "--name", containerName)

	// Add environment variables
	for k, v := range env {
		cmdParts = append(cmdParts, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// Add volume mounts
	if mounts, ok := config["Mounts"].([]interface{}); ok {
		for _, m := range mounts {
			if mount, ok := m.(map[string]interface{}); ok {
				src := mount["Source"].(string)
				dst := mount["Destination"].(string)
				mode := "rw"
				if rw, ok := mount["RW"].(bool); ok && !rw {
					mode = "ro"
				}
				cmdParts = append(cmdParts, "-v", fmt.Sprintf("%s:%s:%s", src, dst, mode))
			}
		}
	}

	// Add host agent path as volume
	cmdParts = append(cmdParts, "-v", fmt.Sprintf("%s:%s:ro", do.hostAgentPath, DefaultContainerAgentPath))

	// Add ports
	if networkSettings, ok := config["NetworkSettings"].(map[string]interface{}); ok {
		if ports, ok := networkSettings["Ports"].(map[string]interface{}); ok {
			for containerPort, bindings := range ports {
				if bindingList, ok := bindings.([]interface{}); ok && len(bindingList) > 0 {
					if binding, ok := bindingList[0].(map[string]interface{}); ok {
						hostPort := binding["HostPort"]
						cmdParts = append(cmdParts, "-p", fmt.Sprintf("%v:%v", hostPort, containerPort))
					}
				}
			}
		}
	}

	// Add image
	if configSection, ok := config["Config"].(map[string]interface{}); ok {
		if image, ok := configSection["Image"].(string); ok {
			cmdParts = append(cmdParts, image)
		}
	}

	return strings.Join(cmdParts, " ")
}

// runContainer runs a docker run command
func (do *DockerOperations) runContainer(command string) error {
	cmd := exec.CommandContext(do.ctx, "sh", "-c", command)
	return cmd.Run()
}

// modifyComposeFile modifies a docker-compose.yml file to add instrumentation
func (do *DockerOperations) modifyComposeFile(container *discovery.DockerContainer, cfg *config.ProcessConfiguration) error {
	modifier := NewComposeModifier(container.ComposeFile)

	// Parse the compose file
	composeData, err := modifier.Parse()
	if err != nil {
		return fmt.Errorf("failed to parse compose file: %w", err)
	}

	// Check if service exists
	service, exists := composeData.Services[container.ComposeService]
	if !exists {
		return fmt.Errorf("service '%s' not found in compose file", container.ComposeService)
	}

	// Check if already instrumented
	if modifier.isServiceInstrumented(&service) {
		fmt.Printf("   ⚠️  Service '%s' appears to already be instrumented\n", container.ComposeService)
		fmt.Print("   Continue with instrumentation? [y/N]: ")

		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			return fmt.Errorf("instrumentation cancelled by user")
		}
	}

	// Add instrumentation to service
	if err := modifier.addInstrumentation(&service, cfg, do.hostAgentPath); err != nil {
		return fmt.Errorf("failed to add instrumentation: %w", err)
	}

	// Update the service in the compose data
	composeData.Services[container.ComposeService] = service

	// Write the modified compose file
	if err := modifier.Write(composeData); err != nil {
		return fmt.Errorf("failed to write modified compose file: %w", err)
	}

	fmt.Printf("   ✅ Modified %s\n", filepath.Base(container.ComposeFile))
	return nil
}

// Parse reads and parses the docker-compose file
func (cm *ComposeModifier) Parse() (*ComposeFile, error) {
	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read compose file: %w", err)
	}

	var composeFile ComposeFile
	if err := yaml.Unmarshal(data, &composeFile); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Initialize maps if they don't exist
	if composeFile.Services == nil {
		composeFile.Services = make(map[string]Service)
	}

	return &composeFile, nil
}

// Write writes the modified compose file back to disk
func (cm *ComposeModifier) Write(composeFile *ComposeFile) error {
	// Convert back to YAML with proper formatting
	data, err := yaml.Marshal(composeFile)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	pp.Println("=== MODIFIED COMPOSE FILE CONTENT ===")
	pp.Println(string(data))
	pp.Println("=== END MODIFIED CONTENT ===")

	// Write to file
	if err := os.WriteFile(cm.filePath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// DEBUG: Read back what was actually written to disk
	actualContent, readErr := os.ReadFile(cm.filePath)
	if readErr == nil {
		fmt.Println("=== ACTUAL FILE CONTENT ON DISK ===")
		fmt.Println(string(actualContent))
		fmt.Println("=== END ACTUAL CONTENT ===")
	}

	return nil
}

// isServiceInstrumented checks if service already has MW instrumentation
func (cm *ComposeModifier) isServiceInstrumented(service *Service) bool {
	// Check environment variables for existing instrumentation
	for _, env := range service.Environment {
		if strings.HasPrefix(env, "MW_API_KEY=") ||
			strings.HasPrefix(env, "OTEL_SERVICE_NAME=") ||
			strings.Contains(env, "javaagent") {
			return true
		}
	}

	// Check volumes for agent mount
	for _, volume := range service.Volumes {
		if strings.Contains(volume, "/opt/middleware/agents/") {
			return true
		}
	}

	return false
}

// addInstrumentation adds MW instrumentation to a service
func (cm *ComposeModifier) addInstrumentation(service *Service, cfg *config.ProcessConfiguration, hostAgentPath string) error {
	// Build environment variables
	mwEnv := cfg.ToEnvironmentVariables()

	// Add JAVA_TOOL_OPTIONS
	javaToolOptions := fmt.Sprintf("JAVA_TOOL_OPTIONS=-javaagent:%s", DefaultContainerAgentPath)

	// Remove any existing MW_ or OTEL_ or JAVA_TOOL_OPTIONS environment variables
	var cleanedEnv []string
	for _, env := range service.Environment {
		if !strings.HasPrefix(env, "MW_") &&
			!strings.HasPrefix(env, "OTEL_") &&
			!strings.HasPrefix(env, "JAVA_TOOL_OPTIONS=") {
			cleanedEnv = append(cleanedEnv, env)
		}
	}

	// Add new environment variables
	cleanedEnv = append(cleanedEnv, javaToolOptions)
	for key, value := range mwEnv {
		cleanedEnv = append(cleanedEnv, fmt.Sprintf("%s=%s", key, value))
	}

	service.Environment = cleanedEnv

	// Add agent volume mount
	agentMount := fmt.Sprintf("%s:%s:ro", hostAgentPath, DefaultContainerAgentPath)

	// Check if agent volume already exists
	agentMountExists := false
	for _, volume := range service.Volumes {
		if strings.Contains(volume, DefaultContainerAgentPath) {
			agentMountExists = true
			break
		}
	}

	if !agentMountExists {
		if service.Volumes == nil {
			service.Volumes = []string{}
		}
		service.Volumes = append(service.Volumes, agentMount)
	}

	return nil
}

// BackupComposeFile creates a backup of the original compose file
func (cm *ComposeModifier) BackupComposeFile() (string, error) {
	backupPath := cm.filePath + ".backup"

	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read original file: %w", err)
	}

	if err := os.WriteFile(backupPath, data, 0o644); err != nil {
		return "", fmt.Errorf("failed to create backup: %w", err)
	}

	return backupPath, nil
}

// RestoreFromBackup restores the compose file from backup
func (cm *ComposeModifier) RestoreFromBackup(backupPath string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	if err := os.WriteFile(cm.filePath, data, 0o644); err != nil {
		return fmt.Errorf("failed to restore from backup: %w", err)
	}

	return nil
}

// ValidateComposeFile validates that the compose file is syntactically correct
func (cm *ComposeModifier) ValidateComposeFile() error {
	_, err := cm.Parse()
	return err
}

func (do *DockerOperations) recreateComposeService(container *discovery.DockerContainer) error {
	if container.ComposeWorkDir == "" {
		return fmt.Errorf("compose working directory not found")
	}

	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	if err := os.Chdir(container.ComposeWorkDir); err != nil {
		return err
	}

	fmt.Printf("   Working directory: %s\n", container.ComposeWorkDir)

	// SOLUTION: Stop and remove the existing container first
	fmt.Println("   Stopping existing container...")
	stopCmd := exec.CommandContext(do.ctx, "docker-compose", "stop", container.ComposeService)
	if output, err := stopCmd.CombinedOutput(); err != nil {
		fmt.Printf("   Warning: Failed to stop container: %s\n", string(output))
	}

	fmt.Println("   Removing existing container...")
	rmCmd := exec.CommandContext(do.ctx, "docker-compose", "rm", "-f", container.ComposeService)
	if output, err := rmCmd.CombinedOutput(); err != nil {
		fmt.Printf("   Warning: Failed to remove container: %s\n", string(output))
	}

	// Now recreate with fresh container
	fmt.Println("   Creating new container...")
	cmd := exec.CommandContext(do.ctx, "docker-compose", "up", "-d", container.ComposeService)

	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("   Docker-compose error output: %s\n", string(output))
		return err
	}

	return nil
}

// saveContainerState saves instrumented container state
func (do *DockerOperations) saveContainerState(container *discovery.DockerContainer, cfg *config.ProcessConfiguration) error {
	state, _ := do.loadState()
	if state.Containers == nil {
		state.Containers = make(map[string]ContainerState)
	}

	state.Containers[container.ContainerName] = ContainerState{
		ContainerID:    container.ContainerID,
		ContainerName:  container.ContainerName,
		ImageName:      container.ImageName,
		InstrumentedAt: time.Now(),
		AgentPath:      do.hostAgentPath,
		OriginalEnv:    container.Environment,
		ComposeFile:    container.ComposeFile,
		ComposeService: container.ComposeService,
	}
	state.UpdatedAt = time.Now()

	return do.saveState(state)
}

// removeContainerState removes container from state
func (do *DockerOperations) removeContainerState(containerName string) error {
	state, _ := do.loadState()
	delete(state.Containers, containerName)
	state.UpdatedAt = time.Now()
	return do.saveState(state)
}

// loadState loads the instrumented containers state
func (do *DockerOperations) loadState() (*InstrumentedState, error) {
	if _, err := os.Stat(StateFile); os.IsNotExist(err) {
		return &InstrumentedState{
			Containers: make(map[string]ContainerState),
		}, nil
	}

	data, err := os.ReadFile(StateFile)
	if err != nil {
		return nil, err
	}

	var state InstrumentedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// saveState saves the instrumented containers state
func (do *DockerOperations) saveState(state *InstrumentedState) error {
	// Ensure directory exists
	dir := filepath.Dir(StateFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(StateFile, data, 0o644)
}

// copyFile copies a file from src to dst
func (do *DockerOperations) copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// ListInstrumentedContainers lists all instrumented containers
func (do *DockerOperations) ListInstrumentedContainers() ([]ContainerState, error) {
	state, err := do.loadState()
	if err != nil {
		return nil, err
	}

	var containers []ContainerState
	for _, c := range state.Containers {
		containers = append(containers, c)
	}

	return containers, nil
}

// verifyContainerInstrumentation checks if instrumentation actually worked
func (do *DockerOperations) verifyContainerInstrumentation(containerName string) error {
	// Get container info
	containerInfo, err := do.cli.ContainerInspect(do.ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	// Check if JAVA_TOOL_OPTIONS is set correctly in environment
	javaOptsFound := false
	for _, env := range containerInfo.Config.Env {
		if strings.HasPrefix(env, "JAVA_TOOL_OPTIONS=") && strings.Contains(env, "javaagent") {
			javaOptsFound = true
			break
		}
	}

	if !javaOptsFound {
		return fmt.Errorf("JAVA_TOOL_OPTIONS not set correctly")
	}

	// Verify agent mount exists
	agentMountFound := false
	for _, mount := range containerInfo.HostConfig.Binds {
		if strings.Contains(mount, DefaultContainerAgentPath) {
			agentMountFound = true
			break
		}
	}

	if !agentMountFound {
		return fmt.Errorf("agent mount not found")
	}

	return nil
}
