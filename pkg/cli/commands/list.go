package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/middleware-labs/java-injector/pkg/cli/types"
	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// ListCommand lists all Java processes on the host
type ListCommand struct {
	config *types.CommandConfig
}

func NewListCommand(config *types.CommandConfig) *ListCommand {
	return &ListCommand{config: config}
}

func (c *ListCommand) Execute() error {
	ctx := context.Background()

	processes, err := discovery.FindAllJavaProcesses(ctx)
	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	if len(processes) == 0 {
		fmt.Println("No Java processes found")
		return nil
	}

	fmt.Printf("Found %d Java processes:\n\n", len(processes))

	for _, proc := range processes {
		// pp.Println(proc)
		fmt.Printf("PID: %d\n", proc.ProcessPID)
		fmt.Printf("  Service: %s\n", proc.ServiceName)
		fmt.Printf("  Owner: %s\n", proc.ProcessOwner)
		fmt.Printf("  Agent: %s\n", proc.FormatAgentStatus())

		if proc.HasJavaAgent {
			agentInfo := proc.GetAgentInfo()
			fmt.Printf("  Agent Path: %s\n", agentInfo.Path)
		}

		// Check if Tomcat
		if proc.IsTomcat() {
			tomcatInfo := proc.ExtractTomcatInfo()
			fmt.Printf("  Type: Tomcat\n")
			fmt.Printf("  Instance: %s\n", tomcatInfo.InstanceName)
			if len(tomcatInfo.Webapps) > 0 {
				fmt.Printf("  Webapps: %v\n", tomcatInfo.Webapps)
			}
		}

		// Check if configured
		configPath := c.getConfigPath(&proc)
		if c.fileExists(configPath) {
			fmt.Printf("  Config: ✅ %s\n", configPath)
		} else {
			fmt.Printf("  Config: ❌ Not configured\n")
		}

		fmt.Println()
	}

	return nil
}

func (c *ListCommand) GetDescription() string {
	return "List all Java processes running on the host"
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

	containers, err := discoverer.DiscoverJavaContainers()
	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	if len(containers) == 0 {
		fmt.Println("No Java Docker containers found")
		return nil
	}

	fmt.Printf("Found %d Java Docker containers:\n\n", len(containers))

	for _, container := range containers {
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
			fmt.Printf("  Status: ✅ Instrumented\n")
		} else {
			fmt.Printf("  Status: ⚠️  Not instrumented\n")
		}

		fmt.Println()
	}

	return nil
}

func (c *ListDockerCommand) GetDescription() string {
	return "List all Java Docker containers"
}

// ListAllCommand lists both host processes and Docker containers
type ListAllCommand struct {
	listCommand       *ListCommand
	listDockerCommand *ListDockerCommand
}

func NewListAllCommand(config *types.CommandConfig) *ListAllCommand {
	return &ListAllCommand{
		listCommand:       NewListCommand(config),
		listDockerCommand: NewListDockerCommand(config),
	}
}

func (c *ListAllCommand) Execute() error {
	fmt.Println("=" + strings.Repeat("=", 70))
	fmt.Println("HOST JAVA PROCESSES")
	fmt.Println("=" + strings.Repeat("=", 70))

	if err := c.listCommand.Execute(); err != nil {
		return err
	}

	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("DOCKER JAVA CONTAINERS")
	fmt.Println(strings.Repeat("=", 70))

	return c.listDockerCommand.Execute()
}

func (c *ListAllCommand) GetDescription() string {
	return "List both host processes and Docker containers"
}

// Helper methods (these will be moved to appropriate packages in later steps)
func (c *ListCommand) getConfigPath(proc *discovery.JavaProcess) string {
	serviceName := c.generateServiceName(proc)

	if proc.IsTomcat() {
		return fmt.Sprintf("/etc/middleware/tomcat/%s.conf", serviceName)
	}

	deploymentType := c.detectDeploymentType(proc)
	return fmt.Sprintf("/etc/middleware/%s/%s.conf", deploymentType, serviceName)
}

func (c *ListCommand) detectDeploymentType(proc *discovery.JavaProcess) string {
	if proc.ProcessOwner != "root" && proc.ProcessOwner != os.Getenv("USER") {
		return "systemd"
	}
	return "standalone"
}

func (c *ListCommand) generateServiceName(proc *discovery.JavaProcess) string {
	// Simplified version - full implementation will be moved to naming package
	if proc.JarFile != "" {
		return strings.TrimSuffix(proc.JarFile, ".jar")
	}
	if proc.ServiceName != "" && proc.ServiceName != "java-service" {
		return proc.ServiceName
	}
	return fmt.Sprintf("java-app-%d", proc.ProcessPID)
}

func (c *ListCommand) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
