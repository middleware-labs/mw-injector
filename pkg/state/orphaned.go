// Package state provides JSON-based state persistence for tracking instrumented
// services at /etc/middleware/state/. It also detects orphaned configurations
// (drop-in files left behind after a process exits) and cleans them up.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/middleware-labs/java-injector/pkg/discovery"
	"github.com/middleware-labs/java-injector/pkg/naming"
	"github.com/middleware-labs/java-injector/pkg/systemd"
)

// FindOrphanedConfigs detects configuration files for stopped/crashed services
// Moved from main.go and commands/uninstrument.go (consolidated duplicate implementations)
func FindOrphanedConfigs(runningProcesses []*discovery.Process) ([]OrphanedConfig, error) {
	var orphaned []OrphanedConfig

	// Get all running process config paths
	runningConfigs := make(map[string]bool)
	for _, proc := range runningProcesses {
		configPath := getConfigPath(proc)
		runningConfigs[configPath] = true
	}

	// Check systemd configs
	if configs, err := scanConfigDirectory("/etc/middleware/systemd", false, runningConfigs); err == nil {
		orphaned = append(orphaned, configs...)
	}

	// Check tomcat configs
	if configs, err := scanConfigDirectory("/etc/middleware/tomcat", true, runningConfigs); err == nil {
		orphaned = append(orphaned, configs...)
	}

	// Check standalone configs
	if configs, err := scanConfigDirectory("/etc/middleware/standalone", false, runningConfigs); err == nil {
		orphaned = append(orphaned, configs...)
	}

	return orphaned, nil
}

// scanConfigDirectory scans a specific config directory for orphaned configs
func scanConfigDirectory(dir string, isTomcat bool, runningConfigs map[string]bool) ([]OrphanedConfig, error) {
	var orphaned []OrphanedConfig

	if !fileExists(dir) {
		return orphaned, nil
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".conf") {
			configPath := filepath.Join(dir, file.Name())
			if !runningConfigs[configPath] {
				serviceName := strings.TrimSuffix(file.Name(), ".conf")
				orphaned = append(orphaned, OrphanedConfig{
					ConfigPath:  configPath,
					ServiceName: serviceName,
					IsTomcat:    isTomcat,
				})
			}
		}
	}

	return orphaned, nil
}

// RemoveOrphanedConfig removes an orphaned configuration and its associated files
// Moved from main.go and commands/uninstrument.go (consolidated duplicate implementations)
func RemoveOrphanedConfig(config OrphanedConfig) error {
	// Remove config file
	if err := os.Remove(config.ConfigPath); err != nil {
		return fmt.Errorf("failed to remove config file: %w", err)
	}
	fmt.Printf("   Removed config: %s\n", config.ConfigPath)

	// Determine systemd service name
	var serviceName string
	if config.IsTomcat {
		serviceName = systemd.GetTomcatServiceName()
	} else {
		serviceName = config.ServiceName + ".service"
	}

	// Remove systemd drop-in
	if err := systemd.RemoveDropIn(serviceName); err != nil {
		fmt.Printf("   Warning: Failed to remove systemd drop-in: %v\n", err)
	}

	fmt.Printf("   🗑️  Removed orphaned instrumentation for: %s\n", config.ServiceName)
	return nil
}

// ScanAndSaveOrphaned scans for orphaned configs and saves the state
func ScanAndSaveOrphaned(runningProcesses []*discovery.Process) error {
	orphaned, err := FindOrphanedConfigs(runningProcesses)
	if err != nil {
		return fmt.Errorf("failed to find orphaned configs: %w", err)
	}

	state := &HostState{
		OrphanedConfigs: orphaned,
	}

	if err := SaveHostState(state); err != nil {
		return fmt.Errorf("failed to save host state: %w", err)
	}

	return nil
}

// GetOrphanedConfigsFromState loads orphaned configs from saved state
func GetOrphanedConfigsFromState() ([]OrphanedConfig, error) {
	state, err := LoadHostState()
	if err != nil {
		return nil, fmt.Errorf("failed to load host state: %w", err)
	}

	return state.OrphanedConfigs, nil
}

// CleanupOrphanedConfig removes an orphaned config and updates the state
func CleanupOrphanedConfig(config OrphanedConfig) error {
	// Remove the orphaned config
	if err := RemoveOrphanedConfig(config); err != nil {
		return err
	}

	// Update the state by removing this config from the list
	state, err := LoadHostState()
	if err != nil {
		// If we can't load state, that's OK - the config is still removed
		fmt.Printf("   Warning: Could not update state: %v\n", err)
		return nil
	}

	// Remove the config from the state
	var updatedConfigs []OrphanedConfig
	for _, oc := range state.OrphanedConfigs {
		if oc.ConfigPath != config.ConfigPath {
			updatedConfigs = append(updatedConfigs, oc)
		}
	}
	state.OrphanedConfigs = updatedConfigs

	// Save updated state
	if err := SaveHostState(state); err != nil {
		fmt.Printf("   Warning: Could not save updated state: %v\n", err)
	}

	return nil
}

// ValidateOrphanedConfigs validates that orphaned configs actually exist
func ValidateOrphanedConfigs(configs []OrphanedConfig) []OrphanedConfig {
	var validated []OrphanedConfig

	for _, config := range configs {
		if fileExists(config.ConfigPath) {
			validated = append(validated, config)
		}
	}

	return validated
}

// Helper functions

// getConfigPath generates the config path for a Java process
func getConfigPath(proc *discovery.Process) string {
	serviceName := naming.GenerateServiceName(proc)

	if proc.DetailBool(discovery.DetailIsTomcat) {
		return fmt.Sprintf("/etc/middleware/tomcat/%s.conf", serviceName)
	}

	deploymentType := detectDeploymentType(proc)
	return fmt.Sprintf("/etc/middleware/%s/%s.conf", deploymentType, serviceName)
}

// detectDeploymentType determines the deployment type for a process
func detectDeploymentType(proc *discovery.Process) string {
	if proc.Owner != "root" && proc.Owner != os.Getenv("USER") {
		return "systemd"
	}
	return "standalone"
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
