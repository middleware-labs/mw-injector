// Package systemd provides utilities for managing systemd services: querying
// status, restarting units, and cleaning up drop-in configuration files
// created during instrumentation.
package systemd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/middleware-labs/java-injector/pkg/discovery"
	"github.com/middleware-labs/java-injector/pkg/naming"
)

// GetServiceName tries to find the actual systemd service name for a Java process
// Moved from main.go: getSystemdServiceName()
func GetServiceName(proc *discovery.Process) string {
	// Try to find the actual systemd service by PID
	cmd := exec.Command("systemctl", "status", fmt.Sprintf("%d", proc.PID))
	output, err := cmd.CombinedOutput()

	if err == nil {
		// Parse output to find service name
		lines := strings.Split(string(output), "\n")
		if len(lines) > 0 {
			firstLine := lines[0]
			if strings.HasPrefix(firstLine, "●") || strings.HasPrefix(firstLine, "○") {
				parts := strings.Fields(firstLine)
				if len(parts) >= 2 {
					return parts[1]
				}
			}
		}
	}

	// Fallback: try common service name patterns
	serviceName := naming.GenerateServiceName(proc)
	possibleNames := []string{
		"spring-boot.service",
		serviceName + ".service",
		proc.ServiceName + ".service",
	}

	for _, name := range possibleNames {
		cmd := exec.Command("systemctl", "status", name)
		if err := cmd.Run(); err == nil {
			return name
		}
	}

	return serviceName + ".service"
}

// GetServiceStatus returns the status of a systemd service
func GetServiceStatus(serviceName string) (string, error) {
	cmd := exec.Command("systemctl", "is-active", serviceName)
	output, err := cmd.Output()
	if err != nil {
		return "unknown", err
	}

	return strings.TrimSpace(string(output)), nil
}

// RestartService restarts a systemd service
func RestartService(serviceName string) error {
	cmd := exec.Command("systemctl", "restart", serviceName)
	return cmd.Run()
}

// ReloadSystemd reloads the systemd daemon
func ReloadSystemd() error {
	cmd := exec.Command("systemctl", "daemon-reload")
	return cmd.Run()
}

// ServiceExists checks if a systemd service exists
func ServiceExists(serviceName string) bool {
	cmd := exec.Command("systemctl", "status", serviceName)
	err := cmd.Run()
	return err == nil
}

// RemoveDropIn removes a systemd drop-in file and its parent directory if empty.
// The drop-in is expected at /etc/systemd/system/{serviceName}.d/middleware-instrumentation.conf.
func RemoveDropIn(serviceName string) error {
	dropInDir := fmt.Sprintf("/etc/systemd/system/%s.d", serviceName)
	dropInPath := filepath.Join(dropInDir, "middleware-instrumentation.conf")

	if fileExists(dropInPath) {
		if err := os.Remove(dropInPath); err != nil {
			return fmt.Errorf("failed to remove drop-in file: %v", err)
		}
		fmt.Printf("   Removed drop-in: %s\n", dropInPath)

		// Remove directory if empty
		files, _ := os.ReadDir(dropInDir)
		if len(files) == 0 {
			if err := os.Remove(dropInDir); err == nil {
				fmt.Printf("   Removed empty directory: %s\n", dropInDir)
			}
		}
	}

	return nil
}

// fileExists checks if a file exists at the given path.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// GetTomcatServiceName returns the default systemd service name for Tomcat.
func GetTomcatServiceName() string {
	return "tomcat.service"
}

// IsTomcatService checks if a service name refers to a Tomcat service.
func IsTomcatService(serviceName string) bool {
	return strings.Contains(strings.ToLower(serviceName), "tomcat")
}
