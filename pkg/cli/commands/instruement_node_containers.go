package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/k0kubun/pp"
	"github.com/middleware-labs/java-injector/pkg/agent"
	"github.com/middleware-labs/java-injector/pkg/cli/types"
	"github.com/middleware-labs/java-injector/pkg/discovery"
	"github.com/middleware-labs/java-injector/pkg/docker"
	"github.com/middleware-labs/java-injector/pkg/systemd"
)

type ConfigInstrumentNodeDockerCommand struct {
	config     *types.CommandConfig
	configPath string
}

func NewConfigInstrumentNodeDockerCommand(
	config *types.CommandConfig,
	configPath string,
) *ConfigInstrumentNodeDockerCommand {
	if configPath == "" {
		configPath = findDefaultConfigFile()
	}

	return &ConfigInstrumentNodeDockerCommand{
		config:     config,
		configPath: configPath,
	}
}

func (c *ConfigInstrumentNodeDockerCommand) Execute() error {
	ctx := context.Background()
	if os.Geteuid() != 0 {
		return fmt.Errorf("❌ This command requires root privileges\n   Run with: sudo mw-injector instrument-docker-config [config-file]")
	}

	// Check if config file was found/provided
	if c.configPath == "" {
		return fmt.Errorf("❌ No config file found. Please create /etc/mw-injector.conf or provide path:\n   Usage: mw-injector instrument-docker-config <config-file>")
	}

	configVars, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("❌ Failed to load config from %s: %v", c.configPath, err)
	}

	pp.Println("Print me some vars yo!!!:")
	pp.Println(configVars)
	// Validate required config
	apiKey := configVars["MW_API_KEY"]
	if apiKey == "" {
		return fmt.Errorf("❌ MW_API_KEY is required in config file")
	}

	target := configVars["MW_TARGET"]
	if target == "" {
		target = "https://prod.middleware.io:443"
	}

	agentPath := configVars["MW_NODE_AGENT"]
	pp.Println("WHATS WITH THE AGENT PATH YO!!!:", agentPath)

	if agentPath == "" {
		agentPath = c.config.DefaultAgentPath
	}

	pp.Println("^^^^WHATS WITH THE AGENT PATH YO!!!:", agentPath)

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

	fmt.Printf("\n🔍 Found %d NodeJS Docker containers\n\n", len(containers))
	fmt.Printf("✅ Using agent at: %s\n\n", installedPath)

	configured := 0
	updated := 0
	skipped := 0

	dockerOps := docker.NewDockerOperations(ctx, installedPath)

	pp.Println(c)
	return nil
}

func (c *ConfigInstrumentNodeDockerCommand) GetDescription() string {
	return "Auto-instrument all NodeJs Docker containers using config file"
}

// loadConfig loads configuration from the specified file path
func (c *ConfigInstrumentNodeDockerCommand) loadConfig() (map[string]string, error) {
	return systemd.ReadConfigFile(c.configPath)
}
