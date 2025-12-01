package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/k0kubun/pp"
	"github.com/middleware-labs/java-injector/pkg/cli/types"
	"github.com/middleware-labs/java-injector/pkg/discovery"
	"github.com/middleware-labs/java-injector/pkg/docker"
	"github.com/middleware-labs/java-injector/pkg/naming"
	"github.com/middleware-labs/java-injector/pkg/systemd"
)

// UninstrumentCommand removes instrumentation from all host processes
type UninstrumentCommand struct {
	config *types.CommandConfig
}

func NewUninstrumentCommand(config *types.CommandConfig) *UninstrumentCommand {
	return &UninstrumentCommand{config: config}
}

// Update the Execute method to use the new check:
func (c *UninstrumentCommand) Execute() error {
	ctx := context.Background()

	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("❌ This command requires root privileges\n   Run with: sudo mw-injector uninstrument")
	}

	reader := bufio.NewReader(os.Stdin)

	// Discover processes
	processes, err := discovery.FindAllJavaProcesses(ctx)
	if err != nil {
		return fmt.Errorf("❌ Error discovering processes: %v", err)
	}

	if len(processes) == 0 {
		fmt.Println("No Running Java processes found")
	}

	fmt.Printf("\n🔍 Found %d Java processes\n\n", len(processes))

	// Check for orphaned configs (services that are stopped/crashed)
	orphanedConfigs := c.findOrphanedConfigs(processes)

	if len(processes) == 0 && len(orphanedConfigs) == 0 {
		fmt.Println("\nNo instrumented services found")
		return nil
	}

	if len(orphanedConfigs) > 0 {
		fmt.Printf("\n⚠️  Found %d orphaned configuration(s) for stopped/crashed services:\n\n", len(orphanedConfigs))
	}

	removed := 0
	skipped := 0
	servicesToRestart := []string{}

	// Process orphaned configs first
	for _, orphan := range orphanedConfigs {
		fmt.Printf("⚠️  Orphaned config found\n")
		fmt.Printf("   Service: %s (%s)\n", orphan.ServiceName, orphan.ConfigPath)
		if orphan.IsTomcat {
			fmt.Printf("   Type: Tomcat (service may be crashed)\n")
		} else {
			fmt.Printf("   Type: Systemd service (service may be stopped)\n")
		}
		fmt.Print("   Remove instrumentation? [y/N]: ")

		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response == "y" || response == "yes" {
			c.removeOrphanedConfig(orphan)
			removed++

			// Add to restart list
			if orphan.IsTomcat {
				servicesToRestart = append(servicesToRestart, "tomcat.service")
			} else {
				servicesToRestart = append(servicesToRestart, orphan.ServiceName+".service")
			}
		} else {
			skipped++
		}
		fmt.Println()
	}

	// Now process running processes
	if len(processes) > 0 {
		fmt.Printf("\n📋 Processing running services:\n\n")
	}

	for _, proc := range processes {
		// Check if configured (either config file OR drop-in exists)
		if !c.isInstrumented(&proc) {
			fmt.Printf("⭐️  Skipping PID %d (%s) - not instrumented\n", proc.ProcessPID, proc.ServiceName)
			skipped++
			continue
		}

		fmt.Printf("⚠️  PID %d (%s) is instrumented\n", proc.ProcessPID, proc.ServiceName)
		fmt.Print("   Remove instrumentation? [y/N]: ")

		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response != "y" && response != "yes" {
			fmt.Printf("⭐️  Skipping PID %d (%s)\n\n", proc.ProcessPID, proc.ServiceName)
			skipped++
			continue
		}

		// Remove config file if it exists
		configPath := c.getConfigPath(&proc)
		if c.fileExists(configPath) {
			if err := os.Remove(configPath); err != nil {
				fmt.Printf("❌ Failed to remove config for PID %d: %v\n", proc.ProcessPID, err)
				continue
			}
			fmt.Printf("   Removed config: %s\n", configPath)
		}

		// Remove systemd drop-in
		var systemdServiceName string
		if proc.IsTomcat() {
			systemdServiceName = systemd.GetTomcatServiceName()
		} else {
			systemdServiceName = systemd.GetServiceName(&proc)
		}

		// Remove systemd drop-in file
		if err := systemd.RemoveDropIn(systemdServiceName); err != nil {
			fmt.Printf("⚠️  Warning: Failed to remove systemd drop-in: %v\n", err)
		} else {
			fmt.Printf("   Removed drop-in: /etc/systemd/system/%s.d/middleware-instrumentation.conf\n", systemdServiceName)
		}

		if proc.IsTomcat() {
			fmt.Printf("🗑️  Removed instrumentation from Tomcat\n")
		} else {
			serviceName := naming.GenerateServiceName(&proc)
			fmt.Printf("🗑️  Removed instrumentation from: %s\n", serviceName)
		}

		servicesToRestart = append(servicesToRestart, systemdServiceName)
		removed++
		fmt.Println()
	}

	fmt.Printf("\n🎉 Uninstrumentation complete!\n")
	fmt.Printf("   Removed: %d\n", removed)
	fmt.Printf("   Skipped: %d\n", skipped)
	fmt.Printf("   Total: %d\n", len(processes))

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

func (c *UninstrumentCommand) GetDescription() string {
	return "Uninstrument all host Java processes"
}

// UninstrumentDockerCommand removes instrumentation from all Docker containers
type UninstrumentDockerCommand struct {
	config *types.CommandConfig
}

func NewUninstrumentDockerCommand(config *types.CommandConfig) *UninstrumentDockerCommand {
	return &UninstrumentDockerCommand{config: config}
}

func (c *UninstrumentDockerCommand) Execute() error {
	ctx := context.Background()

	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("❌ This command requires root privileges\n   Run with: sudo mw-injector uninstrument-docker")
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Uninstrument ALL Docker containers? [y/N]: ")
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response != "y" && response != "yes" {
		fmt.Println("Cancelled")
		return nil
	}

	dockerOps := docker.NewDockerOperations(ctx, c.config.DefaultAgentPath)

	// List instrumented containers
	instrumented, err := dockerOps.ListInstrumentedContainers()
	pp.Println("Instrumented Containers: ", instrumented)
	if err != nil {
		return fmt.Errorf("❌ Error listing instrumented containers: %v", err)
	}

	if len(instrumented) == 0 {
		fmt.Println("No instrumented Docker containers found")
		return nil
	}

	fmt.Printf("\n🔧 Uninstrumenting %d containers...\n\n", len(instrumented))

	success := 0
	failed := 0

	for _, container := range instrumented {
		err := dockerOps.UninstrumentContainer(container.ContainerName)
		if err != nil {
			fmt.Printf("❌ Failed to uninstrument %s: %v\n", container.ContainerName, err)
			failed++
		} else {
			success++
		}
	}

	fmt.Printf("\n🎉 Uninstrumentation complete!\n")
	fmt.Printf("   Success: %d\n", success)
	fmt.Printf("   Failed: %d\n", failed)

	return nil
}

func (c *UninstrumentDockerCommand) GetDescription() string {
	return "Uninstrument all Docker containers"
}

// UninstrumentContainerCommand removes instrumentation from a specific Docker container
type UninstrumentContainerCommand struct {
	config        *types.CommandConfig
	containerName string
}

func NewUninstrumentContainerCommand(config *types.CommandConfig) *UninstrumentContainerCommand {
	return &UninstrumentContainerCommand{config: config}
}

func (c *UninstrumentContainerCommand) SetArg(arg string) {
	c.containerName = arg
}

func (c *UninstrumentContainerCommand) Execute() error {
	ctx := context.Background()

	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("❌ This command requires root privileges\n   Run with: sudo mw-injector uninstrument-container %s", c.containerName)
	}

	dockerOps := docker.NewDockerOperations(ctx, c.config.DefaultAgentPath)

	fmt.Printf("🔧 Uninstrumenting container: %s\n\n", c.containerName)

	if err := dockerOps.UninstrumentContainer(c.containerName); err != nil {
		return fmt.Errorf("❌ Failed to uninstrument container: %v", err)
	}

	fmt.Println("🎉 Container uninstrumented successfully!")
	return nil
}

func (c *UninstrumentContainerCommand) GetDescription() string {
	return "Uninstrument a specific Docker container"
}

// Helper types and methods (temporary until we create the state package)
type OrphanedConfig struct {
	ConfigPath  string
	ServiceName string
	IsTomcat    bool
}

// Helper methods (these will be moved to appropriate packages in later steps)
func (c *UninstrumentCommand) getConfigPath(proc *discovery.JavaProcess) string {
	serviceName := naming.GenerateServiceName(proc)

	if proc.IsTomcat() {
		return fmt.Sprintf("/etc/middleware/tomcat/%s.conf", serviceName)
	}

	deploymentType := c.detectDeploymentType(proc)
	return fmt.Sprintf("/etc/middleware/%s/%s.conf", deploymentType, serviceName)
}

func (c *UninstrumentCommand) detectDeploymentType(proc *discovery.JavaProcess) string {
	if proc.ProcessOwner != "root" && proc.ProcessOwner != os.Getenv("USER") {
		return "systemd"
	}
	return "standalone"
}

func (c *UninstrumentCommand) generateServiceName(proc *discovery.JavaProcess) string {
	// Use the naming package
	return naming.GenerateServiceName(proc)
}

func (c *UninstrumentCommand) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (c *UninstrumentCommand) getSystemdServiceName(proc *discovery.JavaProcess) string {
	// Use the systemd package
	return systemd.GetServiceName(proc)
}

func (c *UninstrumentCommand) findOrphanedConfigs(runningProcesses []discovery.JavaProcess) []OrphanedConfig {
	var orphaned []OrphanedConfig

	// Get all running process config paths
	runningConfigs := make(map[string]bool)
	for _, proc := range runningProcesses {
		configPath := c.getConfigPath(&proc)
		runningConfigs[configPath] = true
	}

	// Check systemd configs
	systemdDir := "/etc/middleware/systemd"
	if c.fileExists(systemdDir) {
		files, _ := os.ReadDir(systemdDir)
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".conf") {
				configPath := fmt.Sprintf("%s/%s", systemdDir, file.Name())
				if !runningConfigs[configPath] {
					serviceName := strings.TrimSuffix(file.Name(), ".conf")
					orphaned = append(orphaned, OrphanedConfig{
						ConfigPath:  configPath,
						ServiceName: serviceName,
						IsTomcat:    false,
					})
				}
			}
		}
	}

	// Check tomcat configs
	tomcatDir := "/etc/middleware/tomcat"
	if c.fileExists(tomcatDir) {
		files, _ := os.ReadDir(tomcatDir)
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".conf") {
				configPath := fmt.Sprintf("%s/%s", tomcatDir, file.Name())
				if !runningConfigs[configPath] {
					instanceName := strings.TrimSuffix(file.Name(), ".conf")
					orphaned = append(orphaned, OrphanedConfig{
						ConfigPath:  configPath,
						ServiceName: instanceName,
						IsTomcat:    true,
					})
				}
			}
		}
	}

	// Check standalone configs
	standaloneDir := "/etc/middleware/standalone"
	if c.fileExists(standaloneDir) {
		files, _ := os.ReadDir(standaloneDir)
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".conf") {
				configPath := fmt.Sprintf("%s/%s", standaloneDir, file.Name())
				if !runningConfigs[configPath] {
					serviceName := strings.TrimSuffix(file.Name(), ".conf")
					orphaned = append(orphaned, OrphanedConfig{
						ConfigPath:  configPath,
						ServiceName: serviceName,
						IsTomcat:    false,
					})
				}
			}
		}
	}

	return orphaned
}

func (c *UninstrumentCommand) removeOrphanedConfig(config OrphanedConfig) {
	// Remove config file
	if err := os.Remove(config.ConfigPath); err != nil {
		fmt.Printf("   ❌ Failed to remove config: %v\n", err)
		return
	}
	fmt.Printf("   Removed config: %s\n", config.ConfigPath)

	// TODO: Remove systemd drop-in files
	// This will be implemented when we create the systemd package

	fmt.Printf("   🗑️  Removed orphaned instrumentation for: %s\n", config.ServiceName)
}

func (c *UninstrumentCommand) getDropInPath(proc *discovery.JavaProcess) string {
	var systemdServiceName string
	if proc.IsTomcat() {
		systemdServiceName = systemd.GetTomcatServiceName()
	} else {
		systemdServiceName = systemd.GetServiceName(proc)
	}

	return fmt.Sprintf("/etc/systemd/system/%s.d/middleware-instrumentation.conf", systemdServiceName)
}

func (c *UninstrumentCommand) isInstrumented(proc *discovery.JavaProcess) bool {
	// Check if EITHER config file OR drop-in file exists
	configPath := c.getConfigPath(proc)
	dropInPath := c.getDropInPath(proc)

	return c.fileExists(configPath) || c.fileExists(dropInPath)
}
