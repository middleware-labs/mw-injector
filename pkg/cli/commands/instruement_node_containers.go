package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/k0kubun/pp"
	"github.com/middleware-labs/java-injector/pkg/agent"
	"github.com/middleware-labs/java-injector/pkg/cli/types"
	"github.com/middleware-labs/java-injector/pkg/config"
	"github.com/middleware-labs/java-injector/pkg/discovery"
	"github.com/middleware-labs/java-injector/pkg/docker"
	"github.com/middleware-labs/java-injector/pkg/systemd"
)

type ConfigNodeContainerCommand struct {
	config     *types.CommandConfig
	configPath string
}

func NewConfigNodeContainerCommand(
	config *types.CommandConfig,
	configPath string,
) *ConfigNodeContainerCommand {
	if configPath == "" {
		configPath = findDefaultConfigFile()
	}

	return &ConfigNodeContainerCommand{
		config:     config,
		configPath: configPath,
	}
}

func (c *ConfigNodeContainerCommand) Execute() error {
	ctx := context.Background()

	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("❌ This command requires root privileges\n   Run with: sudo mw-injector instrument-docker-config [config-file]")
	}

	// Check if config file was found/provided
	if c.configPath == "" {
		return fmt.Errorf("❌ No config file found. Please create /etc/mw-injector.conf or provide path:\n   Usage: mw-injector instrument-docker-config <config-file>")
	}

	// Load configuration from file
	configVars, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("❌ Failed to load config from %s: %v", c.configPath, err)
	}

	// Validate required config
	apiKey := configVars["MW_API_KEY"]
	if apiKey == "" {
		return fmt.Errorf("❌ MW_API_KEY is required in config file")
	}

	target := configVars["MW_TARGET"]
	if target == "" {
		target = "https://prod.middleware.io:443"
	}

	agentPath := configVars["MW_NODE_AGENT_PATH"]
	if agentPath == "" {
		agentPath = c.config.DefaultAgentPath
	}

	fmt.Printf("🔧 Using configuration from: %s\n", c.configPath)
	fmt.Printf("   API Key: %s...\n", apiKey[:min(8, len(apiKey))])
	fmt.Printf("   Target: %s\n", target)
	fmt.Printf("   Agent Path: %s\n", agentPath)

	// Ensure agent is installed and accessible
	installedPath, err := agent.EnsureInstalled(agentPath, c.config.DefaultNodeAgentPath)
	if err != nil {
		return fmt.Errorf("❌ Failed to prepare agent: %v", err)
	}

	// Discover Docker containers
	discoverer := discovery.NewDockerDiscoverer(ctx)
	containers, err := discoverer.DiscoverNodeContainers()
	if err != nil {
		return fmt.Errorf("❌ Error discovering containers: %v", err)
	}

	if len(containers) == 0 {
		fmt.Println("No NodeJS Docker containers found")
		return nil
	}

	fmt.Printf("\n🔍 Found %d Nodejs Docker containers\n\n", len(containers))
	fmt.Printf("✅ Using agent at: %s\n\n", installedPath)

	configured := 0
	updated := 0
	skipped := 0

	dockerOps := docker.NewDockerOperations(ctx, installedPath)

	for _, container := range containers {
		if container.Instrumented && container.IsMiddlewareAgent {
			fmt.Printf("✅ Container %s is already instrumented\n", container.ContainerName)
			fmt.Printf("   📝 Auto-updating configuration...\n")
			updated++
		} else {
			fmt.Printf("🎯 Container %s - %s:%s\n", container.ContainerName, container.ImageName, container.ImageTag)
			fmt.Printf("   📝 Auto-configuring for instrumentation...\n")
			configured++
		}
		if _, err := os.Stat(agentPath); err != nil {
			pp.Printf("❌ agent file does not exist: %s", agentPath)
			skipped++
			continue
		}

		// Create configuration
		cfg := config.DefaultConfiguration()
		cfg.MWAPIKey = apiKey
		cfg.MWTarget = target
		cfg.MWServiceName = container.GetServiceName()
		// cfg.OtelServiceName = container.GetServiceName()
		cfg.NodeAgentPath = docker.DefaultContainerAgentNodePath

		// Instrument container
		err := dockerOps.InstrumentContainer(container.ContainerName, &cfg)
		if err != nil {
			fmt.Printf("❌ Failed to instrument container %s: %v\n", container.ContainerName, err)
			skipped++
		} else {
			fmt.Printf("   ✅ Successfully instrumented container\n")
		}
		fmt.Println()
	}
	return nil
}

// loadConfig loads configuration from the specified file path
func (c *ConfigNodeContainerCommand) loadConfig() (map[string]string, error) {
	return systemd.ReadConfigFile(c.configPath)
}
