package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DockerContainer represents a discovered Docker container running Java
type DockerContainer struct {
	// Container identification
	ContainerID   string    `json:"container.id"`
	ContainerName string    `json:"container.name"`
	ImageName     string    `json:"container.image.name"`
	ImageTag      string    `json:"container.image.tag"`
	Created       time.Time `json:"container.created"`
	Status        string    `json:"container.status"`

	// Container runtime info
	Command     string            `json:"container.command"`
	Entrypoint  []string          `json:"container.entrypoint"`
	Environment map[string]string `json:"container.environment"`
	Labels      map[string]string `json:"container.labels"`

	// Java detection
	IsJava        bool     `json:"java.detected"`
	JavaProcesses []string `json:"java.processes,omitempty"`
	JarFiles      []string `json:"java.jar_files,omitempty"`

	IsNodeJS bool `json:"node.detected"`
	// Instrumentation detection
	HasJavaAgent      bool   `json:"java.agent.present"`
	JavaAgentPath     string `json:"java.agent.path,omitempty"`
	IsMiddlewareAgent bool   `json:"middleware.agent.detected"`

	// OTEL Configuration
	OTELServiceName string `json:"otel.service.name,omitempty"`
	OTELEndpoint    string `json:"otel.exporter.otlp.endpoint,omitempty"`
	OTELHeaders     string `json:"otel.exporter.otlp.headers,omitempty"`

	// Docker Compose detection
	IsCompose      bool   `json:"docker.compose.detected"`
	ComposeProject string `json:"docker.compose.project,omitempty"`
	ComposeService string `json:"docker.compose.service,omitempty"`
	ComposeFile    string `json:"docker.compose.file,omitempty"`
	ComposeWorkDir string `json:"docker.compose.workdir,omitempty"`

	// Network and ports
	Networks []string          `json:"container.networks"`
	Ports    map[string]string `json:"container.ports"`

	// Volumes
	Mounts []DockerMount `json:"container.mounts"`

	// Metadata
	Instrumented   bool      `json:"instrumented"`
	InstrumentedAt time.Time `json:"instrumented_at,omitempty"`
}

// DockerMount represents a Docker volume mount
type DockerMount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	RW          bool   `json:"rw"`
}

// DockerDiscoverer handles Docker container discovery
type DockerDiscoverer struct {
	ctx context.Context
}

// NewDockerDiscoverer creates a new Docker discoverer
func NewDockerDiscoverer(ctx context.Context) *DockerDiscoverer {
	return &DockerDiscoverer{ctx: ctx}
}

func (dd *DockerDiscoverer) DiscoverNodeContainers() ([]DockerContainer, error) {
	//TODO: fix this ugly duplication. Some sort of DiscoverXContainers(x) where x being langs or
	// frameworks
	if !dd.isDockerAvailable() {
		return nil, fmt.Errorf("docker is not available or not running")
	}

	// Get all running containers
	containers, err := dd.listRunningContainers()
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Filter and inspect Java containers
	var nodeContainers []DockerContainer
	for _, containerID := range containers {
		container, err := dd.inspectContainer(containerID)
		if err != nil {
			continue // Skip containers we can't inspect
		}
		// TODO: check above todo.
		// Check if container runs node
		if dd.isNodeJSContainer(container) {
			nodeContainers = append(nodeContainers, *container)
		}
	}

	return nodeContainers, nil
}

// DiscoverJavaContainers finds all running Docker containers with Java
func (dd *DockerDiscoverer) DiscoverJavaContainers() ([]DockerContainer, error) {
	// Check if Docker is available
	if !dd.isDockerAvailable() {
		return nil, fmt.Errorf("docker is not available or not running")
	}

	// Get all running containers
	containers, err := dd.listRunningContainers()
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Filter and inspect Java containers
	var javaContainers []DockerContainer
	for _, containerID := range containers {
		container, err := dd.inspectContainer(containerID)
		if err != nil {
			continue // Skip containers we can't inspect
		}

		// Check if container runs Java
		if dd.isJavaContainer(container) {
			javaContainers = append(javaContainers, *container)
		}
	}

	return javaContainers, nil
}

// isDockerAvailable checks if Docker daemon is accessible
func (dd *DockerDiscoverer) isDockerAvailable() bool {
	cmd := exec.CommandContext(dd.ctx, "docker", "info")
	err := cmd.Run()
	return err == nil
}

// listRunningContainers lists all running container IDs
func (dd *DockerDiscoverer) listRunningContainers() ([]string, error) {
	cmd := exec.CommandContext(dd.ctx, "docker", "ps", "-q", "--no-trunc")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	containerIDs := strings.Split(strings.TrimSpace(string(output)), "\n")
	var result []string
	for _, id := range containerIDs {
		if id != "" {
			result = append(result, id)
		}
	}

	return result, nil
}

// inspectContainer gets detailed information about a container
func (dd *DockerDiscoverer) inspectContainer(containerID string) (*DockerContainer, error) {
	cmd := exec.CommandContext(dd.ctx, "docker", "inspect", containerID)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	// Parse Docker inspect JSON
	var inspectData []map[string]interface{}
	if err := json.Unmarshal(output, &inspectData); err != nil {
		return nil, fmt.Errorf("failed to parse inspect output: %w", err)
	}

	if len(inspectData) == 0 {
		return nil, fmt.Errorf("no data returned for container %s", containerID)
	}

	data := inspectData[0]
	container := &DockerContainer{
		ContainerID: containerID,
		Environment: make(map[string]string),
		Labels:      make(map[string]string),
		Ports:       make(map[string]string),
	}

	// Extract basic info
	if name, ok := data["Name"].(string); ok {
		container.ContainerName = strings.TrimPrefix(name, "/")
	}

	// Extract config
	if config, ok := data["Config"].(map[string]interface{}); ok {
		dd.parseConfig(container, config)
	}

	// Extract state
	if state, ok := data["State"].(map[string]interface{}); ok {
		dd.parseState(container, state)
	}

	// Extract network settings
	if networkSettings, ok := data["NetworkSettings"].(map[string]interface{}); ok {
		dd.parseNetworkSettings(container, networkSettings)
	}

	// Extract mounts
	if mounts, ok := data["Mounts"].([]interface{}); ok {
		dd.parseMounts(container, mounts)
	}

	// Detect Docker Compose
	dd.detectDockerCompose(container)

	// Detect instrumentation
	dd.detectContainerInstrumentation(container)

	// Get Java processes inside container
	dd.detectJavaProcesses(container)

	return container, nil
}

// parseConfig parses the Config section of docker inspect
func (dd *DockerDiscoverer) parseConfig(container *DockerContainer, config map[string]interface{}) {
	// Image
	if image, ok := config["Image"].(string); ok {
		parts := strings.Split(image, ":")
		container.ImageName = parts[0]
		if len(parts) > 1 {
			container.ImageTag = parts[1]
		} else {
			container.ImageTag = "latest"
		}
	}

	// Environment variables
	if env, ok := config["Env"].([]interface{}); ok {
		for _, e := range env {
			if envStr, ok := e.(string); ok {
				parts := strings.SplitN(envStr, "=", 2)
				if len(parts) == 2 {
					container.Environment[parts[0]] = parts[1]
				}
			}
		}
	}

	// Labels
	if labels, ok := config["Labels"].(map[string]interface{}); ok {
		for k, v := range labels {
			if vStr, ok := v.(string); ok {
				container.Labels[k] = vStr
			}
		}
	}

	// Command
	if cmd, ok := config["Cmd"].([]interface{}); ok {
		var cmdParts []string
		for _, c := range cmd {
			if cStr, ok := c.(string); ok {
				cmdParts = append(cmdParts, cStr)
			}
		}
		container.Command = strings.Join(cmdParts, " ")
	}

	// Entrypoint
	if entrypoint, ok := config["Entrypoint"].([]interface{}); ok {
		for _, e := range entrypoint {
			if eStr, ok := e.(string); ok {
				container.Entrypoint = append(container.Entrypoint, eStr)
			}
		}
	}
}

// parseState parses the State section of docker inspect
func (dd *DockerDiscoverer) parseState(container *DockerContainer, state map[string]interface{}) {
	if status, ok := state["Status"].(string); ok {
		container.Status = status
	}

	if startedAt, ok := state["StartedAt"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, startedAt); err == nil {
			container.Created = t
		}
	}
}

// parseNetworkSettings parses network configuration
func (dd *DockerDiscoverer) parseNetworkSettings(container *DockerContainer, networkSettings map[string]interface{}) {
	// Networks
	if networks, ok := networkSettings["Networks"].(map[string]interface{}); ok {
		for networkName := range networks {
			container.Networks = append(container.Networks, networkName)
		}
	}

	// Ports
	if ports, ok := networkSettings["Ports"].(map[string]interface{}); ok {
		for port, bindings := range ports {
			if bindingList, ok := bindings.([]interface{}); ok && len(bindingList) > 0 {
				if binding, ok := bindingList[0].(map[string]interface{}); ok {
					hostIP := binding["HostIp"]
					hostPort := binding["HostPort"]
					container.Ports[port] = fmt.Sprintf("%v:%v", hostIP, hostPort)
				}
			}
		}
	}
}

// parseMounts parses volume mounts
func (dd *DockerDiscoverer) parseMounts(container *DockerContainer, mounts []interface{}) {
	for _, m := range mounts {
		if mount, ok := m.(map[string]interface{}); ok {
			dockerMount := DockerMount{}

			if t, ok := mount["Type"].(string); ok {
				dockerMount.Type = t
			}
			if src, ok := mount["Source"].(string); ok {
				dockerMount.Source = src
			}
			if dst, ok := mount["Destination"].(string); ok {
				dockerMount.Destination = dst
			}
			if mode, ok := mount["Mode"].(string); ok {
				dockerMount.Mode = mode
			}
			if rw, ok := mount["RW"].(bool); ok {
				dockerMount.RW = rw
			}

			container.Mounts = append(container.Mounts, dockerMount)
		}
	}
}

func (dd *DockerDiscoverer) detectDockerCompose(container *DockerContainer) {
	if project, ok := container.Labels["com.docker.compose.project"]; ok {
		container.IsCompose = true
		container.ComposeProject = project
	}

	if service, ok := container.Labels["com.docker.compose.service"]; ok {
		container.ComposeService = service
	}

	if workdir, ok := container.Labels["com.docker.compose.project.working_dir"]; ok {
		container.ComposeWorkDir = workdir

		// Search for compose file with multiple possible names
		possibleFiles := []string{
			"docker-compose.yml",
			"docker-compose.yaml",
			"compose.yml",
			"compose.yaml",
		}

		for _, filename := range possibleFiles {
			fullPath := filepath.Join(workdir, filename)
			if _, err := os.Stat(fullPath); err == nil {
				container.ComposeFile = fullPath
				break
			}
		}
	}
}

// detectContainerInstrumentation checks if container already has Java agent
func (dd *DockerDiscoverer) detectContainerInstrumentation(container *DockerContainer) {
	// Check JAVA_TOOL_OPTIONS
	if javaToolOptions, ok := container.Environment["JAVA_TOOL_OPTIONS"]; ok {
		if strings.Contains(javaToolOptions, "-javaagent:") {
			container.HasJavaAgent = true
			agentPath := dd.extractAgentPathFromEnv(javaToolOptions)
			container.JavaAgentPath = agentPath

			// Check if it's Middleware agent
			if dd.isMiddlewareAgent(agentPath) {
				container.IsMiddlewareAgent = true
				container.Instrumented = true
			}
		}
	}

	// Check OTEL configuration
	if serviceName, ok := container.Environment["OTEL_SERVICE_NAME"]; ok {
		container.OTELServiceName = serviceName
	}
	if endpoint, ok := container.Environment["OTEL_EXPORTER_OTLP_ENDPOINT"]; ok {
		container.OTELEndpoint = endpoint
	}
	if headers, ok := container.Environment["OTEL_EXPORTER_OTLP_HEADERS"]; ok {
		container.OTELHeaders = headers
	}

	// Check MW_* environment variables
	if _, ok := container.Environment["MW_API_KEY"]; ok {
		container.Instrumented = true
	}
}

// extractAgentPathFromEnv extracts javaagent path from JAVA_TOOL_OPTIONS
func (dd *DockerDiscoverer) extractAgentPathFromEnv(envValue string) string {
	if idx := strings.Index(envValue, "-javaagent:"); idx != -1 {
		agentPart := envValue[idx+len("-javaagent:"):]
		if spaceIdx := strings.Index(agentPart, " "); spaceIdx != -1 {
			agentPart = agentPart[:spaceIdx]
		}
		// Remove agent arguments if present (agent.jar=arg1,arg2)
		if eqIdx := strings.Index(agentPart, "="); eqIdx != -1 {
			return agentPart[:eqIdx]
		}
		return agentPart
	}
	return ""
}

// isMiddlewareAgent checks if agent path indicates Middleware agent
func (dd *DockerDiscoverer) isMiddlewareAgent(agentPath string) bool {
	agentPathLower := strings.ToLower(agentPath)
	middlewarePatterns := []string{
		"middleware",
		"mw-",
		"mw.jar",
		"middleware-javaagent",
		"mw-javaagent",
	}

	for _, pattern := range middlewarePatterns {
		if strings.Contains(agentPathLower, pattern) {
			return true
		}
	}
	return false
}

/* TODO: -------------------------------------------
 Check and Fix Potential Issues:
* 		- False Positives: Could detect non-Java containers that just have Java keywords in names
*		- Side Effects: The function both returns a boolean AND modifies the container object
*		- Error Handling: The dynamic check (hasJavaProcessInside) might fail silently
*/

// isJavaContainer checks if container runs Java
func (dd *DockerDiscoverer) isJavaContainer(container *DockerContainer) bool {
	// Check 1: Image name contains java
	if strings.Contains(strings.ToLower(container.ImageName), "java") ||
		strings.Contains(strings.ToLower(container.ImageName), "openjdk") ||
		strings.Contains(strings.ToLower(container.ImageName), "jdk") ||
		strings.Contains(strings.ToLower(container.ImageName), "jre") {
		container.IsJava = true
		return true
	}

	// Check 2: Command contains java
	if strings.Contains(strings.ToLower(container.Command), "java") {
		container.IsJava = true
		return true
	}

	// Check 3: Entrypoint contains java
	for _, entry := range container.Entrypoint {
		if strings.Contains(strings.ToLower(entry), "java") {
			container.IsJava = true
			return true
		}
	}

	// Check 4: Environment variables indicate Java
	if _, ok := container.Environment["JAVA_HOME"]; ok {
		container.IsJava = true
		return true
	}
	if _, ok := container.Environment["JAVA_OPTS"]; ok {
		container.IsJava = true
		return true
	}

	// Check 5: Try to detect Java process inside container (requires exec)
	if dd.hasJavaProcessInside(container.ContainerID) {
		container.IsJava = true
		return true
	}

	return false
}

// hasJavaProcessInside checks if container has Java processes running
func (dd *DockerDiscoverer) hasJavaProcessInside(containerID string) bool {
	cmd := exec.CommandContext(dd.ctx, "docker", "exec", containerID, "sh", "-c", "ps aux | grep java | grep -v grep")
	output, err := cmd.Output()
	return err == nil && len(output) > 0
}

// detectJavaProcesses detects Java processes and JAR files in container
func (dd *DockerDiscoverer) detectJavaProcesses(container *DockerContainer) {
	// Get process list
	cmd := exec.CommandContext(dd.ctx, "docker", "exec", container.ContainerID, "sh", "-c", "ps aux | grep java | grep -v grep")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			container.JavaProcesses = append(container.JavaProcesses, line)

			// Extract JAR files from process command
			jarFiles := dd.extractJarFilesFromCommand(line)
			container.JarFiles = append(container.JarFiles, jarFiles...)
		}
	}
}

func (dd *DockerDiscoverer) isNodeJSContainer(container *DockerContainer) bool {
	// Check 1: Image name contains node
	if strings.Contains(strings.ToLower(container.ImageName), "node") ||
		strings.Contains(strings.ToLower(container.ImageName), "nodejs") {
		container.IsNodeJS = true
		return true
	}

	// Check 2: Command contains node or npm
	commandLower := strings.ToLower(container.Command)
	if strings.Contains(commandLower, "node") ||
		strings.Contains(commandLower, "npm") ||
		strings.Contains(commandLower, "npx") ||
		strings.Contains(commandLower, "yarn") {
		container.IsNodeJS = true
		return true
	}

	// Check 3: Entrypoint contains node
	for _, entry := range container.Entrypoint {
		entryLower := strings.ToLower(entry)
		if strings.Contains(entryLower, "node") ||
			strings.Contains(entryLower, "npm") ||
			strings.Contains(entryLower, "npx") ||
			strings.Contains(entryLower, "yarn") {
			container.IsNodeJS = true
			return true
		}
	}

	// Check 4: Environment variables indicate Node.js
	if _, ok := container.Environment["NODE_ENV"]; ok {
		container.IsNodeJS = true
		return true
	}
	if _, ok := container.Environment["NPM_CONFIG_REGISTRY"]; ok {
		container.IsNodeJS = true
		return true
	}
	if _, ok := container.Environment["YARN_VERSION"]; ok {
		container.IsNodeJS = true
		return true
	}
	if _, ok := container.Environment["NODE_VERSION"]; ok {
		container.IsNodeJS = true
		return true
	}

	// Check 5: Try to detect Node.js process inside container (requires exec)
	if dd.hasNodeJSProcessInside(container.ContainerID) {
		container.IsNodeJS = true
		return true
	}

	return false
}

// hasNodeJSProcessInside checks if container has Node.js processes running
func (dd *DockerDiscoverer) hasNodeJSProcessInside(containerID string) bool {
	cmd := exec.CommandContext(dd.ctx, "docker", "exec", containerID, "sh", "-c", "ps aux | grep -E '(node|npm)' | grep -v grep")
	output, err := cmd.Output()
	return err == nil && len(output) > 0
}

// extractJarFilesFromCommand extracts JAR file paths from command line
func (dd *DockerDiscoverer) extractJarFilesFromCommand(cmdline string) []string {
	var jarFiles []string
	jarPattern := regexp.MustCompile(`[\w/\-\.]+\.jar`)
	matches := jarPattern.FindAllString(cmdline, -1)
	for _, match := range matches {
		if !contains(jarFiles, match) {
			jarFiles = append(jarFiles, match)
		}
	}
	return jarFiles
}

// GetContainerByName finds a container by name
func (dd *DockerDiscoverer) GetContainerByName(name string) (*DockerContainer, error) {
	containers, err := dd.DiscoverJavaContainers()
	if err != nil {
		return nil, err
	}

	for _, container := range containers {
		if container.ContainerName == name {
			return &container, nil
		}
	}

	return nil, fmt.Errorf("container not found: %s", name)
}

// GetContainerByID finds a container by ID
func (dd *DockerDiscoverer) GetContainerByID(id string) (*DockerContainer, error) {
	return dd.inspectContainer(id)
}

// FormatAgentStatus returns human-readable agent status
func (dc *DockerContainer) FormatAgentStatus() string {
	if !dc.HasJavaAgent {
		return "❌ None"
	}

	if dc.IsMiddlewareAgent {
		return "✅ MW"
	}

	return "✅ Other"
}

// GetServiceName returns the service name for the container
func (dc *DockerContainer) GetServiceName() string {
	// Priority 1: OTEL_SERVICE_NAME
	if dc.OTELServiceName != "" {
		return dc.OTELServiceName
	}

	// Priority 2: Docker Compose service name
	if dc.ComposeService != "" {
		return dc.ComposeService
	}

	// Priority 3: Container name
	return dc.ContainerName
}

// contains checks if string slice contains a value
func contains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}
