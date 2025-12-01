package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/middleware-labs/java-injector/pkg/cli/types"
	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// ListSystemdCommand lists systemd Java processes on the host
type ListSystemdCommand struct {
	config *types.CommandConfig
}

func NewListSystemdCommand(config *types.CommandConfig) *ListSystemdCommand {
	return &ListSystemdCommand{config: config}
}

func (c *ListSystemdCommand) Execute() error {
	ctx := context.Background()

	processes, err := discovery.FindAllJavaProcesses(ctx)
	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	// Filter for systemd processes (including Tomcat)
	var systemdProcesses []discovery.JavaProcess
	for _, proc := range processes {
		if c.detectDeploymentType(&proc) == "systemd" {
			systemdProcesses = append(systemdProcesses, proc)
		}
	}

	if len(systemdProcesses) == 0 {
		fmt.Println("No systemd Java processes found")
		return nil
	}

	fmt.Printf("Found %d systemd Java processes:\n\n", len(systemdProcesses))

	for _, proc := range systemdProcesses {
		c.printProcess(&proc)
	}

	return nil
}

func (c *ListSystemdCommand) GetDescription() string {
	return "List systemd Java processes on the host"
}

// ListDockerCommand lists all Java Docker containers
type ListDockerCommand struct {
	config *types.CommandConfig
}

func NewListDockerCommand(config *types.CommandConfig) *ListDockerCommand {
	return &ListDockerCommand{config: config}
}

func (c *ListDockerCommand) Execute() error {
	ctx := context.Background()
	discoverer := discovery.NewDockerDiscoverer(ctx)

	javaContainers, err := discoverer.DiscoverJavaContainers()
	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	if len(javaContainers) == 0 {
		fmt.Println("No Java Docker containers found.\n\n")
	} else {
		fmt.Printf("Found %d Java Docker containers:\n\n", len(javaContainers))
		for _, container := range javaContainers {
			c.printContainer(&container)
		}
	}

	nodeContainers, err := discoverer.DiscoverNodeContainers()
	if err != nil {
		return fmt.Errorf("error: %v in discovering node containers", err)
	}

	if len(nodeContainers) == 0 {
		fmt.Println("No nodejs containers found")
	} else {
		fmt.Printf("Found %d NodeJS Docker containers:\n\n", len(nodeContainers))
		for _, container := range nodeContainers {
			c.printContainer(&container)
		}
	}

	return nil
}

func (c *ListDockerCommand) GetDescription() string {
	return "List all Java Docker containers"
}

func (c *ListDockerCommand) printContainer(container *discovery.DockerContainer) {
	fmt.Printf("Container: %s\n", container.ContainerName)
	fmt.Printf("  ID: %s\n", container.ContainerID[:12])
	fmt.Printf("  Image: %s:%s\n", container.ImageName, container.ImageTag)
	fmt.Printf("  Status: %s\n", container.Status)
	fmt.Printf("  Agent: %s\n", container.FormatAgentStatus())

	if container.HasJavaAgent {
		fmt.Printf("  Agent Path: %s\n", container.JavaAgentPath)
	}

	if container.IsCompose {
		fmt.Printf("  Type: Docker Compose\n")
		fmt.Printf("  Project: %s\n", container.ComposeProject)
		fmt.Printf("  Service: %s\n", container.ComposeService)
	}

	if len(container.JarFiles) > 0 {
		fmt.Printf("  JAR Files: %v\n", container.JarFiles)
	}

	if container.Instrumented {
		fmt.Printf("  Status: [✓] Instrumented\n")
	} else {
		fmt.Printf("  Status: [!] Not instrumented\n")
	}

	fmt.Println()
}

// ListAllCommand lists all Java processes segregated by type
type ListAllCommand struct {
	config *types.CommandConfig
}

func NewListAllCommand(config *types.CommandConfig) *ListAllCommand {
	return &ListAllCommand{config: config}
}

func (c *ListAllCommand) Execute() error {
	ctx := context.Background()

	// Get all host processes
	processes, err := discovery.FindAllJavaProcesses(ctx)
	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	// Get Docker containers
	dockerDiscoverer := discovery.NewDockerDiscoverer(ctx)
	javaContainers, javaDockerErr := dockerDiscoverer.DiscoverJavaContainers()
	nodeContainers, nodeDiscoverErr := dockerDiscoverer.DiscoverNodeContainers()

	// Segregate processes by type
	var standaloneProcs []discovery.JavaProcess
	var systemdProcs []discovery.JavaProcess
	var tomcatProcs []discovery.JavaProcess

	for _, proc := range processes {
		if proc.IsTomcat() {
			tomcatProcs = append(tomcatProcs, proc)
		} else if c.detectDeploymentType(&proc) == "systemd" {
			systemdProcs = append(systemdProcs, proc)
		} else {
			standaloneProcs = append(standaloneProcs, proc)
		}
	}
	// Print header
	c.printHeader()

	// Print Tomcat section
	c.printTomcatSection(tomcatProcs)

	// Print Systemd section
	c.printSystemdSection(systemdProcs)

	// Print Java Docker section
	if javaDockerErr == nil {
		c.printJavaDockerSection(javaContainers)
	} else {
		c.printDockerErrorSection("JAVA", javaDockerErr)
	}

	// Print Node.js Docker section
	if nodeDiscoverErr == nil {
		c.printNodeDockerSection(nodeContainers)
	} else {
		c.printDockerErrorSection("NODE.JS", nodeDiscoverErr)
	}

	// Print summary
	c.printSummary(len(tomcatProcs), len(systemdProcs), len(javaContainers), len(nodeContainers))

	return nil
}

func (c *ListAllCommand) GetDescription() string {
	return "List all Java processes and containers (segregated by type)"
}

func (c *ListAllCommand) printHeader() {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    [RUNTIME] PROCESS INVENTORY                      ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

func (c *ListAllCommand) printTomcatSection(processes []discovery.JavaProcess) {
	fmt.Printf("╔══════════════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║                  [TOMCAT] INSTANCES (Standalone)                     ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════════════════╝\n\n")

	if len(processes) == 0 {
		fmt.Println("  No standalone Tomcat instances found")
		fmt.Println()
		return
	}

	fmt.Printf("  Found %d standalone Tomcat instance(s)\n\n", len(processes))

	for i, proc := range processes {
		c.printTomcatProcess(&proc, i+1, len(processes))
	}
}

func (c *ListAllCommand) printSystemdSection(processes []discovery.JavaProcess) {
	fmt.Printf("╔══════════════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║                  [SYSTEMD] SERVICES (inc. Tomcat)                    ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════════════════╝\n\n")

	if len(processes) == 0 {
		fmt.Println("  No systemd Java services found")
		fmt.Println()
		return
	}

	// Count Tomcat vs non-Tomcat
	tomcatCount := 0
	for _, proc := range processes {
		if proc.IsTomcat() {
			tomcatCount++
		}
	}

	fmt.Printf("  Found %d systemd service(s)", len(processes))
	if tomcatCount > 0 {
		fmt.Printf(" (%d Tomcat, %d other)\n\n", tomcatCount, len(processes)-tomcatCount)
	} else {
		fmt.Println("\n")
	}

	for i, proc := range processes {
		c.printSystemdProcess(&proc, i+1, len(processes))
	}
}

func (c *ListAllCommand) printJavaDockerSection(containers []discovery.DockerContainer) {
	fmt.Printf("╔══════════════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║                    [DOCKER] JAVA CONTAINERS                          ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════════════════╝\n\n")

	if len(containers) == 0 {
		fmt.Println("  No Java Docker containers found")
		fmt.Println()
		return
	}

	fmt.Printf("  Found %d Java container(s)\n\n", len(containers))

	for i, container := range containers {
		c.printDockerContainer(&container, i+1, len(containers), "JAVA")
	}
}

func (c *ListAllCommand) printNodeDockerSection(containers []discovery.DockerContainer) {
	fmt.Printf("╔══════════════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║                   [DOCKER] NODE.JS CONTAINERS                        ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════════════════╝\n\n")

	if len(containers) == 0 {
		fmt.Println("  No Node.js Docker containers found")
		fmt.Println()
		return
	}

	fmt.Printf("  Found %d Node.js container(s)\n\n", len(containers))

	for i, container := range containers {
		c.printDockerContainer(&container, i+1, len(containers), "NODE")
	}
}

func (c *ListAllCommand) printDockerErrorSection(runtime string, err error) {
	fmt.Printf("╔══════════════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║                   [DOCKER] %s CONTAINERS%s║\n", runtime, strings.Repeat(" ", 25-len(runtime)))
	fmt.Printf("╚══════════════════════════════════════════════════════════════════════╝\n\n")
	fmt.Printf("  [!] Unable to list %s Docker containers: %v\n\n", runtime, err)
}

func (c *ListAllCommand) printTomcatProcess(proc *discovery.JavaProcess, index, total int) {
	tomcatInfo := proc.ExtractTomcatInfo()

	// Draw box
	fmt.Printf("  ┌─────────────────────────────────────────────────────────────────┐\n")
	fmt.Printf("  │ [TOMCAT] Instance (%d/%d)%s│\n", index, total, strings.Repeat(" ", 44-len(fmt.Sprintf("%d/%d", index, total))))
	fmt.Printf("  ├─────────────────────────────────────────────────────────────────┤\n")

	fmt.Printf("  │  Instance Name: %-48s│\n", truncate(tomcatInfo.InstanceName, 48))
	fmt.Printf("  │  PID:           %-48d│\n", proc.ProcessPID)
	fmt.Printf("  │  Owner:         %-48s│\n", truncate(proc.ProcessOwner, 48))
	fmt.Printf("  │  Status:        %-48s│\n", truncate(proc.Status, 48))

	// Agent status
	agentStatus := proc.FormatAgentStatus()
	fmt.Printf("  │  Agent:         %-48s│\n", truncate(agentStatus, 48))

	if proc.HasJavaAgent {
		agentInfo := proc.GetAgentInfo()
		fmt.Printf("  │  Agent Path:    %-48s│\n", truncate(agentInfo.Path, 48))
	}

	// Webapps
	if len(tomcatInfo.Webapps) > 0 {
		fmt.Printf("  │  Webapps:       %-48s│\n", truncate(fmt.Sprintf("%d deployed", len(tomcatInfo.Webapps)), 48))
		for _, webapp := range tomcatInfo.Webapps {
			if webapp != "" {
				fmt.Printf("  │                 • %-46s│\n", truncate(webapp, 46))
			}
		}
	}

	configPath := c.getConfigPath(*proc)
	var configStatus string
	if c.fileExists(configPath) {
		configStatus = "[✓] Configured"
	} else {
		configStatus = "[✗] Not configured"
	}
	fmt.Printf("  │  Config:        %-48s│\n", truncate(configStatus, 48))

	if c.fileExists(configPath) {
		fmt.Printf("  │  Path:          %-48s│\n", truncate(configPath, 48))
	}

	fmt.Printf("  └─────────────────────────────────────────────────────────────────┘\n\n")
}

func (c *ListAllCommand) printSystemdProcess(proc *discovery.JavaProcess, index, total int) {
	// Draw box
	fmt.Printf("  ┌─────────────────────────────────────────────────────────────────┐\n")

	// Show type badge (Tomcat or regular Systemd)
	if proc.IsTomcat() {
		fmt.Printf("  │ [TOMCAT] Service (%d/%d)%s│\n", index, total, strings.Repeat(" ", 45-len(fmt.Sprintf("%d/%d", index, total))))
	} else {
		fmt.Printf("  │ [SYSTEMD] Service (%d/%d)%s│\n", index, total, strings.Repeat(" ", 44-len(fmt.Sprintf("%d/%d", index, total))))
	}

	fmt.Printf("  ├─────────────────────────────────────────────────────────────────┤\n")

	fmt.Printf("  │  Service Name:  %-48s│\n", truncate(proc.ServiceName, 48))
	fmt.Printf("  │  PID:           %-48d│\n", proc.ProcessPID)
	fmt.Printf("  │  Owner:         %-48s│\n", truncate(proc.ProcessOwner, 48))
	fmt.Printf("  │  Status:        %-48s│\n", truncate(proc.Status, 48))

	// Show Tomcat-specific info if applicable
	if proc.IsTomcat() {
		tomcatInfo := proc.ExtractTomcatInfo()
		if tomcatInfo.InstanceName != "" {
			fmt.Printf("  │  Instance:      %-48s│\n", truncate(tomcatInfo.InstanceName, 48))
		}
		if len(tomcatInfo.Webapps) > 0 {
			fmt.Printf("  │  Webapps:       %-48s│\n", truncate(fmt.Sprintf("%d deployed", len(tomcatInfo.Webapps)), 48))
			for _, webapp := range tomcatInfo.Webapps {
				if webapp != "" {
					fmt.Printf("  │                 • %-46s│\n", truncate(webapp, 46))
				}
			}
		}
	} else {
		// JAR info for non-Tomcat services
		if proc.JarFile != "" {
			fmt.Printf("  │  JAR File:      %-48s│\n", truncate(proc.JarFile, 48))
		}
		if proc.MainClass != "" {
			fmt.Printf("  │  Main Class:    %-48s│\n", truncate(proc.MainClass, 48))
		}
	}

	// Agent status
	agentStatus := proc.FormatAgentStatus()
	fmt.Printf("  │  Agent:         %-46s│\n", truncate(agentStatus, 46))

	if proc.HasJavaAgent {
		agentInfo := proc.GetAgentInfo()
		fmt.Printf("  │  Agent Path:    %-48s│\n", truncate(agentInfo.Path, 48))
	}

	// Config status
	configPath := c.getConfigPath(*proc)
	if c.fileExists(configPath) {
		fmt.Printf("  │  Config:        [✓] %-44s│\n", truncate("Configured", 44))
		fmt.Printf("  │  Path:          %-48s│\n", truncate(configPath, 48))
	} else {
		fmt.Printf("  │  Config:        [✗] %-44s│\n", truncate("Not configured", 44))
	}

	fmt.Printf("  └─────────────────────────────────────────────────────────────────┘\n\n")
}

func (c *ListAllCommand) printDockerContainer(container *discovery.DockerContainer, index, total int, runtime string) {
	// Draw box
	fmt.Printf("  ┌─────────────────────────────────────────────────────────────────┐\n")
	fmt.Printf("  │ [%s] Container (%d/%d)%s│\n", runtime, index, total, strings.Repeat(" ", 43-len(fmt.Sprintf("%s] Container (%d/%d", runtime, index, total))))
	fmt.Printf("  ├─────────────────────────────────────────────────────────────────┤\n")

	fmt.Printf("  │  Name:          %-48s│\n", truncate(container.ContainerName, 48))
	fmt.Printf("  │  ID:            %-48s│\n", truncate(container.ContainerID[:12], 48))
	fmt.Printf("  │  Image:         %-48s│\n", truncate(fmt.Sprintf("%s:%s", container.ImageName, container.ImageTag), 48))
	fmt.Printf("  │  Status:        %-48s│\n", truncate(container.Status, 48))

	// Show runtime type
	if container.IsJava {
		fmt.Printf("  │  Runtime:       %-48s│\n", truncate("Java", 48))
	}
	if container.IsNodeJS {
		fmt.Printf("  │  Runtime:       %-48s│\n", truncate("Node.js", 48))
	}

	// Compose info
	if container.IsCompose {
		fmt.Printf("  │  Type:          %-48s│\n", truncate("Docker Compose", 48))
		fmt.Printf("  │  Project:       %-48s│\n", truncate(container.ComposeProject, 48))
		fmt.Printf("  │  Service:       %-48s│\n", truncate(container.ComposeService, 48))
	}

	// Agent status (mainly for Java containers)
	if container.IsJava {
		agentStatus := container.FormatAgentStatus()
		fmt.Printf("  │  Agent:         %-48s│\n", truncate(agentStatus, 48))

		if container.HasJavaAgent {
			fmt.Printf("  │  Agent Path:    %-48s│\n", truncate(container.JavaAgentPath, 48))
		}

		// JAR files
		if len(container.JarFiles) > 0 {
			fmt.Printf("  │  JAR Files:     %-48s│\n", truncate(fmt.Sprintf("%d found", len(container.JarFiles)), 48))
		}

		// Instrumentation status
		if container.Instrumented {
			fmt.Printf("  │  Status:        [✓] %-44s│\n", truncate("Instrumented", 44))
		} else {
			fmt.Printf("  │  Status:        [!] %-44s│\n", truncate("Not instrumented", 44))
		}
	} else if container.IsNodeJS {

		// Node.js specific info (for future use when Node.js agents are added)
		if container.Instrumented {
			fmt.Printf("  │  Agent:         %-48s│\n", truncate("✅ MW", 48))
			fmt.Printf("  │  Status:        [!] %-44s│\n", truncate("Instrumented", 44))
		} else {
			fmt.Printf("  │  Agent:         %-48s│\n", truncate("❌ None", 48))
			fmt.Printf("  │  Status:        [!] %-44s│\n", truncate("Not instrumented", 44))
		}

	}

	fmt.Printf("  └─────────────────────────────────────────────────────────────────┘\n\n")
}

func (c *ListAllCommand) printSummary(tomcatCount, systemdCount, javaDockerCount, nodeDockerCount int) {
	totalCount := tomcatCount + systemdCount + javaDockerCount + nodeDockerCount

	fmt.Printf("╔══════════════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║                            📊 SUMMARY                                ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════════════════╝\n\n")

	fmt.Printf("  Total Processes:         %d\n", totalCount)
	fmt.Printf("    🐱 Standalone Tomcat:   %d\n", tomcatCount)
	fmt.Printf("    🔧 Systemd Services:    %d\n", systemdCount)
	fmt.Printf("    ☕ Java Containers:     %d\n", javaDockerCount)
	fmt.Printf("    🟢 Node.js Containers:  %d\n", nodeDockerCount)
	fmt.Println()
}

// Helper methods for ListSystemdCommand
func (c *ListSystemdCommand) printProcess(proc *discovery.JavaProcess) {
	fmt.Printf("PID: %d\n", proc.ProcessPID)
	fmt.Printf("  Service: %s\n", proc.ServiceName)

	// Show if it's Tomcat
	if proc.IsTomcat() {
		tomcatInfo := proc.ExtractTomcatInfo()
		fmt.Printf("  Type: [TOMCAT]\n")
		if tomcatInfo.InstanceName != "" {
			fmt.Printf("  Instance: %s\n", tomcatInfo.InstanceName)
		}
		if len(tomcatInfo.Webapps) > 0 {
			fmt.Printf("  Webapps: %v\n", tomcatInfo.Webapps)
		}
	}

	fmt.Printf("  Owner: %s\n", proc.ProcessOwner)
	fmt.Printf("  Agent: %s\n", proc.FormatAgentStatus())

	if proc.HasJavaAgent {
		agentInfo := proc.GetAgentInfo()
		fmt.Printf("  Agent Path: %s\n", agentInfo.Path)
	}

	// Check if configured
	configPath := c.getConfigPath(proc)
	if c.fileExists(configPath) {
		fmt.Printf("  Config: [✓] %s\n", configPath)
	} else {
		fmt.Printf("  Config: [✗] Not configured\n")
	}

	fmt.Println()
}

func (c *ListSystemdCommand) getConfigPath(proc *discovery.JavaProcess) string {
	serviceName := c.generateServiceName(proc)
	deploymentType := c.detectDeploymentType(proc)
	return fmt.Sprintf("/etc/middleware/%s/%s.conf", deploymentType, serviceName)
}

func (c *ListSystemdCommand) detectDeploymentType(proc *discovery.JavaProcess) string {
	if proc.ProcessOwner != "root" && proc.ProcessOwner != os.Getenv("USER") {
		return "systemd"
	}
	return "standalone"
}

func (c *ListSystemdCommand) generateServiceName(proc *discovery.JavaProcess) string {
	if proc.JarFile != "" {
		return strings.TrimSuffix(proc.JarFile, ".jar")
	}
	if proc.ServiceName != "" && proc.ServiceName != "java-service" {
		return proc.ServiceName
	}
	return fmt.Sprintf("java-app-%d", proc.ProcessPID)
}

func (c *ListSystemdCommand) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Helper methods for ListAllCommand
func (c *ListAllCommand) getConfigPath(proc discovery.JavaProcess) string {
	serviceName := c.generateServiceName(&proc)

	if proc.IsTomcat() {
		return fmt.Sprintf("/etc/middleware/tomcat/%s.conf", serviceName)
	}

	deploymentType := c.detectDeploymentType(&proc)
	return fmt.Sprintf("/etc/middleware/%s/%s.conf", deploymentType, serviceName)
}

func (c *ListAllCommand) detectDeploymentType(proc *discovery.JavaProcess) string {
	if proc.ProcessOwner != "root" && proc.ProcessOwner != os.Getenv("USER") {
		return "systemd"
	}
	return "standalone"
}

func (c *ListAllCommand) generateServiceName(proc *discovery.JavaProcess) string {
	if proc.JarFile != "" {
		return strings.TrimSuffix(proc.JarFile, ".jar")
	}
	if proc.ServiceName != "" && proc.ServiceName != "java-service" {
		return proc.ServiceName
	}
	return fmt.Sprintf("java-app-%d", proc.ProcessPID)
}

func (c *ListAllCommand) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// truncate truncates a string to a maximum length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
