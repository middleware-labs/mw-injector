package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/k0kubun/pp"

	"github.com/middleware-labs/java-injector/pkg/agent"
	"github.com/middleware-labs/java-injector/pkg/cli/types"
	"github.com/middleware-labs/java-injector/pkg/config"
	"github.com/middleware-labs/java-injector/pkg/discovery"
	"github.com/middleware-labs/java-injector/pkg/docker"
	"github.com/middleware-labs/java-injector/pkg/naming"
	"github.com/middleware-labs/java-injector/pkg/systemd"
)

// AutoInstrumentCommand auto-instruments all uninstrumented processes on the host
type AutoInstrumentCommand struct {
	config *types.CommandConfig
}

func NewAutoInstrumentCommand(config *types.CommandConfig) *AutoInstrumentCommand {
	return &AutoInstrumentCommand{config: config}
}

func (c *AutoInstrumentCommand) Execute() error {
	ctx := context.Background()

	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("❌ This command requires root privileges\n   Run with: sudo mw-injector auto-instrument")
	}

	// Get API key and target
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Middleware.io API Key: ")
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)

	if apiKey == "" {
		return fmt.Errorf("❌ API key is required")
	}

	fmt.Print("Target endpoint [https://prod.middleware.io:443]: ")
	target, _ := reader.ReadString('\n')
	target = strings.TrimSpace(target)
	if target == "" {
		target = "https://prod.middleware.io:443"
	}

	fmt.Printf("Java agent path [%s]: \n", c.config.DefaultAgentPath)
	agentPath, _ := reader.ReadString('\n')
	agentPath = strings.TrimSpace(agentPath)
	if agentPath == "" {
		agentPath = c.config.DefaultAgentPath
	}

	// Ensure agent is installed and accessible
	installedPath, err := agent.EnsureInstalled(agentPath, c.config.DefaultAgentPath)
	if err != nil {
		return fmt.Errorf("failed to prepare agent: %w", err)
	}

	// Discover processes
	processes, err := discovery.FindAllJavaProcesses(ctx)
	if err != nil {
		return fmt.Errorf("❌ Error discovering processes: %v", err)
	}

	if len(processes) == 0 {
		fmt.Println("No Java processes found")
		return nil
	}

	fmt.Printf("\n🔍 Found %d Java processes\n\n", len(processes))

	fmt.Printf("\n✅ Using agent at: %s\n", installedPath)
	fmt.Printf("   Permissions: world-readable (0644)\n")
	fmt.Printf("   Owner: root:root\n")
	fmt.Printf("   Accessible by: ALL users\n\n")

	configured := 0
	updated := 0
	skipped := 0
	var servicesToRestart []string

	for _, proc := range processes {
		// Check if agent is accessible by systemd for this specific process
		if _, err := os.Stat(agentPath); err != nil {
			pp.Printf("❌ agent file does not exist: %s\n", agentPath)
			skipped++
			continue
		}
		if err := agent.CheckAccessibleBySystemd(agentPath, proc.ProcessOwner); err != nil {
			fmt.Printf("❌ Skipping PID %d (%s) due to a permission issue.\n", proc.ProcessPID, proc.ServiceName)
			fmt.Printf("   └── Reason: The service user '%s' cannot access the agent file within the systemd security context.\n", proc.ProcessOwner)
			fmt.Printf("   └── To fix, check file permissions and SELinux/AppArmor policies.\n\n")
			fmt.Println("err:", err)
			skipped++
			continue
		}

		configPath := c.getConfigPath(&proc)
		shouldUpdate := false

		// Check if already configured
		if c.fileExists(configPath) {
			fmt.Printf("⚠️  PID %d (%s) is already configured\n", proc.ProcessPID, proc.ServiceName)
			fmt.Print("   Update configuration? [y/N]: ")

			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))

			if response != "y" && response != "yes" {
				fmt.Printf("⭐️  Skipping PID %d (%s)\n\n", proc.ProcessPID, proc.ServiceName)
				skipped++
				continue
			}
			shouldUpdate = true
		}

		// Generate service name and config
		var systemdServiceName string
		if proc.IsTomcat() {
			serviceName := naming.GenerateServiceName(&proc)

			tomcatConfig := &systemd.TomcatConfig{
				InstanceName: serviceName,
				Pattern:      serviceName,
				APIKey:       apiKey,
				Target:       target,
				AgentPath:    agentPath,
			}

			err := systemd.CreateTomcatConfig(configPath, tomcatConfig)
			if err != nil {
				fmt.Printf("❌ Failed to configure PID %d: %v\n", proc.ProcessPID, err)
				continue
			}

			systemdServiceName = systemd.GetTomcatServiceName()

			dropInConfig := &systemd.DropInConfig{
				ServiceName: systemdServiceName,
				ConfigPath:  configPath,
				IsTomcat:    true,
				AgentPath:   agentPath,
			}

			err = systemd.CreateDropIn(dropInConfig)
			if err != nil {
				fmt.Printf("❌ Failed to create systemd drop-in for PID %d: %v\n", proc.ProcessPID, err)
				continue
			}

			if shouldUpdate {
				fmt.Printf("🔄 Updated Tomcat: %s\n", serviceName)
				updated++
			} else {
				fmt.Printf("✅ Configured Tomcat: %s\n", serviceName)
				configured++
			}
		} else if proc.IsInContainer() {
			fmt.Println("Skipping service running inside a container")
			skipped++
		} else {
			serviceName := naming.GenerateServiceName(&proc)
			systemdServiceName = systemd.GetServiceName(&proc)

			standardConfig := &systemd.StandardConfig{
				ServiceName: serviceName,
				APIKey:      apiKey,
				Target:      target,
				AgentPath:   agentPath,
			}

			err := systemd.CreateStandardConfig(configPath, standardConfig)
			if err != nil {
				fmt.Printf("❌ Failed to configure PID %d: %v\n", proc.ProcessPID, err)
				continue
			}

			dropInConfig := &systemd.DropInConfig{
				ServiceName: systemdServiceName,
				ConfigPath:  configPath,
				IsTomcat:    false,
				AgentPath:   agentPath,
			}

			err = systemd.CreateDropIn(dropInConfig)
			if err != nil {
				fmt.Printf("❌ Failed to create systemd drop-in for PID %d: %v\n", proc.ProcessPID, err)
				continue
			}

			if shouldUpdate {
				fmt.Printf("🔄 Updated: %s (service: %s)\n", serviceName, systemdServiceName)
				updated++
			} else {
				fmt.Printf("✅ Configured: %s (service: %s)\n", serviceName, systemdServiceName)
				configured++
			}
		}

		// Add to restart list if not already there
		found := false
		for _, s := range servicesToRestart {
			if s == systemdServiceName {
				found = true
				break
			}
		}
		if !found && systemdServiceName != "" {
			servicesToRestart = append(servicesToRestart, systemdServiceName)
		}
		fmt.Println()
	}

	if skipped > 0 {
		fmt.Printf("\n Auto-instrumentation complete! Skipped %d services \n", skipped)
	} else if skipped == 0 {
		fmt.Printf("\n🎉 Auto-instrumentation complete!\n")
	}
	fmt.Printf("   Configured: %d\n", configured)
	fmt.Printf("   Updated:    %d\n", updated)
	fmt.Printf("   Skipped:    %d\n", skipped)
	fmt.Printf("   Total:      %d\n", len(processes))

	// Restart services
	if len(servicesToRestart) > 0 {
		fmt.Printf("\n🔄 Restarting %d service(s)...\n\n", len(servicesToRestart))

		systemd.ReloadSystemd()

		for _, service := range servicesToRestart {
			fmt.Printf("   Restarting %s...", service)
			err := systemd.RestartService(service)

			if err != nil {
				fmt.Printf(" ❌ Failed\n")
				fmt.Printf("       Error: %v\n", err)
				fmt.Printf("       Try manually: sudo systemctl restart %s\n", service)
			} else {
				fmt.Printf(" ✅ Done\n")
			}
		}
		fmt.Println("\n✅ All services restarted!")
	}

	return nil
}

func (c *AutoInstrumentCommand) GetDescription() string {
	return "Auto-instrument all uninstrumented Java processes on the host"
}

// InstrumentDockerCommand auto-instruments all Java Docker containers
type InstrumentDockerCommand struct {
	config *types.CommandConfig
}

func NewInstrumentDockerCommand(config *types.CommandConfig) *InstrumentDockerCommand {
	return &InstrumentDockerCommand{config: config}
}

func (c *InstrumentDockerCommand) Execute() error {
	ctx := context.Background()

	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("❌ This command requires root privileges\n   Run with: sudo mw-injector instrument-docker")
	}

	// Get API key and target
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Middleware.io API Key: ")
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)

	if apiKey == "" {
		return fmt.Errorf("❌ API key is required")
	}

	fmt.Print("Target endpoint [https://prod.middleware.io:443]: ")
	target, _ := reader.ReadString('\n')
	target = strings.TrimSpace(target)
	if target == "" {
		target = "https://prod.middleware.io:443"
	}

	fmt.Printf("Java agent path [%s]: ", c.config.DefaultAgentPath)
	agentPath, _ := reader.ReadString('\n')
	agentPath = strings.TrimSpace(agentPath)
	if agentPath == "" {
		agentPath = c.config.DefaultAgentPath
	}

	// Ensure agent is installed and accessible
	installedPath, err := agent.EnsureInstalled(agentPath, c.config.DefaultAgentPath)
	if err != nil {
		return fmt.Errorf("❌ Failed to prepare agent: %v", err)
	}

	// Discover Docker containers
	discoverer := discovery.NewDockerDiscoverer(ctx)
	containers, err := discoverer.DiscoverJavaContainers()
	if err != nil {
		return fmt.Errorf("❌ Error discovering containers: %v", err)
	}

	if len(containers) == 0 {
		fmt.Println("No Java Docker containers found")
		return nil
	}

	fmt.Printf("\n🔍 Found %d Java Docker containers\n\n", len(containers))
	fmt.Printf("✅ Using agent at: %s\n\n", installedPath)

	configured := 0
	updated := 0
	skipped := 0

	dockerOps, err := docker.NewDockerOperations(ctx, installedPath)
	if err != nil {
		return fmt.Errorf("X error in createing docker operations, %v", err.Error())
	}

	for _, container := range containers {
		// Skip if already instrumented
		if container.Instrumented && container.IsMiddlewareAgent {
			fmt.Printf("✅ Container %s is already instrumented\n", container.ContainerName)
			fmt.Print("   Update configuration? [y/N]: ")

			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))

			if response != "y" && response != "yes" {
				fmt.Printf("⭐️  Skipping container %s\n\n", container.ContainerName)
				skipped++
				continue
			}
		}

		// Create configuration
		cfg := config.DefaultConfiguration()
		cfg.MWAPIKey = apiKey
		cfg.MWTarget = target
		cfg.MWServiceName = container.GetServiceName()
		cfg.JavaAgentPath = docker.DefaultContainerAgentPath

		if _, err := os.Stat(agentPath); err != nil {
			pp.Printf("❌ agent file does not exist: %s\n", agentPath)
			skipped++
			continue
		}

		// Instrument container
		err := dockerOps.InstrumentContainer(container.ContainerName, &cfg)
		if err != nil {
			fmt.Printf("❌ Failed to instrument container %s: %v\n", container.ContainerName, err)
			skipped++
		} else {
			if container.Instrumented {
				updated++
			} else {
				configured++
			}
		}
		fmt.Println()
	}

	fmt.Printf("\n🎉 Docker instrumentation complete!\n")
	fmt.Printf("   Configured: %d\n", configured)
	fmt.Printf("   Updated: %d\n", updated)
	fmt.Printf("   Skipped: %d\n", skipped)

	if configured > 0 || updated > 0 {
		fmt.Println("\n📊 Containers are now sending telemetry data to Middleware.io")
	}

	return nil
}

func (c *InstrumentDockerCommand) GetDescription() string {
	return "Auto-instrument all Java Docker containers"
}

// InstrumentContainerCommand instruments a specific Docker container
type InstrumentContainerCommand struct {
	config        *types.CommandConfig
	containerName string
}

func NewInstrumentContainerCommand(config *types.CommandConfig) *InstrumentContainerCommand {
	return &InstrumentContainerCommand{config: config}
}

func (c *InstrumentContainerCommand) SetArg(arg string) {
	c.containerName = arg
}

func (c *InstrumentContainerCommand) Execute() error {
	ctx := context.Background()

	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("❌ This command requires root privileges\n   Run with: sudo mw-injector instrument-container %s", c.containerName)
	}

	// Get API key and target
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Middleware.io API Key: ")
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)

	if apiKey == "" {
		return fmt.Errorf("❌ API key is required")
	}

	fmt.Print("Target endpoint [https://prod.middleware.io:443]: ")
	target, _ := reader.ReadString('\n')
	target = strings.TrimSpace(target)
	if target == "" {
		target = "https://prod.middleware.io:443"
	}

	fmt.Printf("Java agent path [%s]: ", c.config.DefaultAgentPath)
	agentPath, _ := reader.ReadString('\n')
	agentPath = strings.TrimSpace(agentPath)
	if agentPath == "" {
		agentPath = c.config.DefaultAgentPath
	}

	// Ensure agent is installed
	installedPath, err := agent.EnsureInstalled(agentPath, c.config.DefaultAgentPath)
	if err != nil {
		return fmt.Errorf("❌ Failed to prepare agent: %v", err)
	}

	// Verify container exists
	discoverer := discovery.NewDockerDiscoverer(ctx)
	container, err := discoverer.GetContainerByName(c.containerName)
	if err != nil {
		return fmt.Errorf("❌ Container not found: %v", err)
	}

	fmt.Printf("\n🔍 Found container: %s\n", container.ContainerName)
	fmt.Printf("   Image: %s:%s\n", container.ImageName, container.ImageTag)
	fmt.Printf("   Status: %s\n\n", container.Status)

	// Create configuration
	cfg := config.DefaultConfiguration()
	cfg.MWAPIKey = apiKey
	cfg.MWTarget = target
	cfg.MWServiceName = container.GetServiceName()
	cfg.JavaAgentPath = docker.DefaultContainerAgentPath

	// Instrument
	dockerOps, err := docker.NewDockerOperations(ctx, installedPath)
	if err != nil {
		return fmt.Errorf("X error in createing docker operations, %v", err.Error())
	}
	if err := dockerOps.InstrumentContainer(c.containerName, &cfg); err != nil {
		return fmt.Errorf("❌ Failed to instrument container: %v", err)
	}

	fmt.Println("\n🎉 Container instrumented successfully!")
	fmt.Println("📊 Container is now sending telemetry data to Middleware.io")
	return nil
}

func (c *InstrumentContainerCommand) GetDescription() string {
	return "Instrument a specific Docker container"
}

// Helper methods (these will be moved to appropriate packages in later steps)
// These are copied from main.go temporarily to keep functionality working
func (c *AutoInstrumentCommand) getConfigPath(proc *discovery.JavaProcess) string {
	serviceName := naming.GenerateServiceName(proc)

	if proc.IsTomcat() {
		return fmt.Sprintf("/etc/middleware/tomcat/%s.conf", serviceName)
	}

	deploymentType := c.detectDeploymentType(proc)
	return fmt.Sprintf("/etc/middleware/%s/%s.conf", deploymentType, serviceName)
}

func (c *ConfigAutoInstrumentCommand) getConfigPath(proc *discovery.JavaProcess) string {
	serviceName := naming.GenerateServiceName(proc)

	if proc.IsTomcat() {
		return fmt.Sprintf("/etc/middleware/tomcat/%s.conf", serviceName)
	}

	deploymentType := c.detectDeploymentType(proc)
	return fmt.Sprintf("/etc/middleware/%s/%s.conf", deploymentType, serviceName)
}

func (c *AutoInstrumentCommand) detectDeploymentType(proc *discovery.JavaProcess) string {
	if proc.ProcessOwner != "root" && proc.ProcessOwner != os.Getenv("USER") {
		return "systemd"
	}
	return "standalone"
}

func (c *ConfigAutoInstrumentCommand) detectDeploymentType(proc *discovery.JavaProcess) string {
	if proc.ProcessOwner != "root" && proc.ProcessOwner != os.Getenv("USER") {
		return "systemd"
	}
	return "standalone"
}

func (c *AutoInstrumentCommand) generateServiceName(proc *discovery.JavaProcess) string {
	// Use the naming package
	return naming.GenerateServiceName(proc)
}

func (c *AutoInstrumentCommand) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (c *ConfigAutoInstrumentCommand) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (c *AutoInstrumentCommand) getSystemdServiceName(proc *discovery.JavaProcess) string {
	// Use the systemd package
	return systemd.GetServiceName(proc)
}

// ConfigAutoInstrumentCommand auto-instruments all uninstrumented processes using config file
type ConfigAutoInstrumentCommand struct {
	config     *types.CommandConfig
	configPath string
}

func NewConfigAutoInstrumentCommand(config *types.CommandConfig, configPath string) *ConfigAutoInstrumentCommand {
	// If no config path provided, try to find default
	if configPath == "" {
		configPath = findDefaultConfigFile()
	}

	return &ConfigAutoInstrumentCommand{
		config:     config,
		configPath: configPath,
	}
}

func (c *ConfigAutoInstrumentCommand) Execute() error {
	ctx := context.Background()
	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("❌ This command requires root privileges\n   Run with: sudo mw-injector auto-instrument-config [config-file]")
	}

	// Check if config file was found/provided
	if c.configPath == "" {
		return fmt.Errorf("❌ No config file found. Please create /etc/mw-injector.conf or provide path:\n   Usage: mw-injector auto-instrument-config <config-file>")
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

	agentPath := configVars["MW_JAVA_AGENT_PATH"]
	if agentPath == "" {
		agentPath = c.config.DefaultAgentPath
	}

	skipSECheck := configVars["SKIP_SE_CHECK"]

	fmt.Printf("🔧 Using configuration from: %s\n", c.configPath)
	fmt.Printf("   API Key: %s...\n", apiKey[:min(8, len(apiKey))])
	fmt.Printf("   Target: %s\n", target)
	fmt.Printf("   Agent Path: %s\n", agentPath)

	// Ensure agent is installed and accessible
	installedPath, err := agent.EnsureInstalled(agentPath, c.config.DefaultAgentPath)
	if err != nil {
		return fmt.Errorf("failed to prepare agent: %w", err)
	}

	// Discover processes
	processes, err := discovery.FindAllJavaProcesses(ctx)
	if err != nil {
		return fmt.Errorf("❌ Error discovering processes: %v", err)
	}

	if len(processes) == 0 {
		fmt.Println("No Java processes found")
		return nil
	}

	fmt.Printf("\n🔍 Found %d Java processes\n\n", len(processes))
	fmt.Printf("✅ Using agent at: %s\n", installedPath)
	fmt.Printf("   Permissions: world-readable (0644)\n")
	fmt.Printf("   Owner: root:root\n")
	fmt.Printf("   Accessible by: ALL users\n\n")

	configured := 0
	updated := 0
	skipped := 0
	var servicesToRestart []string

	for _, proc := range processes {

		// Check if agent is accessible by systemd for this specific process
		if _, err := os.Stat(agentPath); err != nil {
			pp.Printf("❌ agent file does not exist: %s\n", agentPath)
			skipped++
			continue
		}
		if err := agent.CheckAccessibleBySystemd(agentPath, proc.ProcessOwner); err != nil && skipSECheck != "true" {
			fmt.Printf("❌ Skipping PID %d (%s) due to a permission issue.\n", proc.ProcessPID, proc.ServiceName)
			fmt.Printf("   └── Reason: The service user '%s' cannot access the agent file within the systemd security context.\n", proc.ProcessOwner)
			fmt.Printf("   └── To fix, check file permissions and SELinux/AppArmor policies.\n\n")
			// fmt.Println("", err)
			skipped++
			continue
		}

		configPath := c.getConfigPath(&proc)
		shouldUpdate := false

		// Check if already configured (auto-update without asking)
		if c.fileExists(configPath) {
			fmt.Printf("⚠️  PID %d (%s) is already configured\n", proc.ProcessPID, proc.ServiceName)
			fmt.Printf("   📝 Auto-updating configuration...\n")
			shouldUpdate = true
		}

		// Generate service name and config
		var systemdServiceName string
		if proc.IsTomcat() {
			serviceName := naming.GenerateServiceName(&proc)

			tomcatConfig := &systemd.TomcatConfig{
				InstanceName: serviceName,
				Pattern:      serviceName,
				APIKey:       apiKey,
				Target:       target,
				AgentPath:    agentPath,
			}

			err := systemd.CreateTomcatConfig(configPath, tomcatConfig)
			if err != nil {
				fmt.Printf("❌ Failed to configure PID %d: %v\n", proc.ProcessPID, err)
				skipped++
				continue
			}

			systemdServiceName = systemd.GetTomcatServiceName()

			dropInConfig := &systemd.DropInConfig{
				ServiceName: systemdServiceName,
				ConfigPath:  configPath,
				IsTomcat:    true,
				AgentPath:   agentPath,
			}

			err = systemd.CreateDropIn(dropInConfig)
			if err != nil {
				fmt.Printf("❌ Failed to create systemd drop-in for PID %d: %v\n", proc.ProcessPID, err)
				skipped++
				continue
			}

			if shouldUpdate {
				fmt.Printf("🔄 Updated Tomcat: %s\n", serviceName)
				updated++
			} else {
				fmt.Printf("✅ Configured Tomcat: %s\n", serviceName)
				configured++
			}
		} else if proc.IsInContainer() {
			fmt.Println("Skipping service running inside a container.")
			skipped++
		} else {
			serviceName := naming.GenerateServiceName(&proc)
			systemdServiceName = systemd.GetServiceName(&proc)

			standardConfig := &systemd.StandardConfig{
				ServiceName: serviceName,
				APIKey:      apiKey,
				Target:      target,
				AgentPath:   agentPath,
			}

			err := systemd.CreateStandardConfig(configPath, standardConfig)
			if err != nil {
				fmt.Printf("❌ Failed to configure PID %d: %v\n", proc.ProcessPID, err)
				skipped++
				continue
			}

			dropInConfig := &systemd.DropInConfig{
				ServiceName: systemdServiceName,
				ConfigPath:  configPath,
				IsTomcat:    false,
				AgentPath:   agentPath,
			}

			err = systemd.CreateDropIn(dropInConfig)
			if err != nil {
				fmt.Printf("❌ Failed to create systemd drop-in for PID %d: %v\n", proc.ProcessPID, err)
				skipped++
				continue
			}

			if shouldUpdate {
				fmt.Printf("🔄 Updated: %s (service: %s)\n", serviceName, systemdServiceName)
				updated++
			} else {
				fmt.Printf("✅ Configured: %s (service: %s)\n", serviceName, systemdServiceName)
				configured++
			}
		}

		// Add to restart list if not already there
		found := false
		for _, s := range servicesToRestart {
			if s == systemdServiceName {
				found = true
				break
			}
		}
		if !found && systemdServiceName != "" {
			servicesToRestart = append(servicesToRestart, systemdServiceName)
		}
		fmt.Println()
	}

	if skipped > 0 {
		fmt.Printf("\n Auto-instrumentation complete! Skipped %d services\n", skipped)
	} else if skipped == 0 {
		fmt.Printf("\n🎉 Auto-instrumentation complete!\n")
	}

	fmt.Printf("   Configured: %d\n", configured)
	fmt.Printf("   Updated:    %d\n", updated)
	fmt.Printf("   Skipped:    %d\n", skipped)
	fmt.Printf("   Total:      %d\n", len(processes))

	// Auto-restart services without asking
	if len(servicesToRestart) > 0 {
		fmt.Printf("\n🔄 Auto-restarting %d service(s)...\n\n", len(servicesToRestart))

		systemd.ReloadSystemd()

		for _, service := range servicesToRestart {
			fmt.Printf("   Restarting %s...", service)
			err := systemd.RestartService(service)

			if err != nil {
				fmt.Printf(" ❌ Failed\n")
				fmt.Printf("       Error: %v\n", err)
				fmt.Printf("       Try manually: sudo systemctl restart %s\n", service)
			} else {
				fmt.Printf(" ✅ Done\n")
			}
		}
		fmt.Println("\n✅ All services restarted!")
	}

	return nil
}

func (c *ConfigAutoInstrumentCommand) GetDescription() string {
	return "Auto-instrument all uninstrumented Java processes using config file"
}

// ConfigInstrumentDockerCommand auto-instruments all Java Docker containers using config file
type ConfigInstrumentDockerCommand struct {
	config     *types.CommandConfig
	configPath string
}

func NewConfigInstrumentDockerCommand(config *types.CommandConfig, configPath string) *ConfigInstrumentDockerCommand {
	// If no config path provided, try to find default
	if configPath == "" {
		configPath = findDefaultConfigFile()
	}

	return &ConfigInstrumentDockerCommand{
		config:     config,
		configPath: configPath,
	}
}

func (c *ConfigInstrumentDockerCommand) Execute() error {
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

	agentPath := configVars["MW_JAVA_AGENT_PATH"]
	if agentPath == "" {
		agentPath = c.config.DefaultAgentPath
	}

	fmt.Printf("🔧 Using configuration from: %s\n", c.configPath)
	fmt.Printf("   API Key: %s...\n", apiKey[:min(8, len(apiKey))])
	fmt.Printf("   Target: %s\n", target)
	fmt.Printf("   Agent Path: %s\n", agentPath)

	// Ensure agent is installed and accessible
	installedPath, err := agent.EnsureInstalled(agentPath, c.config.DefaultAgentPath)
	if err != nil {
		return fmt.Errorf("❌ Failed to prepare agent: %v", err)
	}

	// Discover Docker containers
	discoverer := discovery.NewDockerDiscoverer(ctx)
	containers, err := discoverer.DiscoverJavaContainers()
	if err != nil {
		return fmt.Errorf("❌ Error discovering containers: %v", err)
	}

	if len(containers) == 0 {
		fmt.Println("No Java Docker containers found")
		return nil
	}

	fmt.Printf("\n🔍 Found %d Java Docker containers\n\n", len(containers))
	fmt.Printf("✅ Using agent at: %s\n\n", installedPath)

	configured := 0
	updated := 0
	skipped := 0

	dockerOps, err := docker.NewDockerOperations(ctx, installedPath)
	if err != nil {
		return fmt.Errorf("X error in createing docker operations, %v", err.Error())
	}

	for _, container := range containers {
		// Auto-update if already instrumented (no prompts)
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
		cfg.JavaAgentPath = docker.DefaultContainerAgentPath

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

	fmt.Printf("\n🎉 Docker instrumentation complete!\n")
	fmt.Printf("   Configured: %d\n", configured)
	fmt.Printf("   Updated: %d\n", updated)
	fmt.Printf("   Skipped: %d\n", skipped)

	if configured > 0 || updated > 0 {
		fmt.Println("\n📊 Containers are now sending telemetry data to Middleware.io")
	}

	return nil
}

func (c *ConfigInstrumentDockerCommand) GetDescription() string {
	return "Auto-instrument all Java Docker containers using config file"
}

// loadConfig loads configuration from the specified file path
func (c *ConfigAutoInstrumentCommand) loadConfig() (map[string]string, error) {
	return systemd.ReadConfigFile(c.configPath)
}

func (c *ConfigInstrumentDockerCommand) loadConfig() (map[string]string, error) {
	return systemd.ReadConfigFile(c.configPath)
}

// Helper function for Go versions that don't have min built-in
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func findDefaultConfigFile() string {
	locations := []string{
		"/etc/mw-injector.conf",
		"/etc/middleware/mw-injector.conf",
	}

	if home := os.Getenv("HOME"); home != "" {
		locations = append(locations, home+"/.mw-injector.conf")
	}

	locations = append(locations, "./mw-injector.conf")

	for _, location := range locations {
		if info, err := os.Stat(location); err == nil && info.Mode().IsRegular() {
			return location
		}
	}

	return ""
}
