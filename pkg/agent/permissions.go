package agent

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// CheckAccessibleByUser tests if a user can access the agent file
// Moved from main.go: isAgentAccessibleByUser()
func CheckAccessibleByUser(agentPath string, username string) error {
	cmd := exec.Command("sudo", "-u", username, "test", "-r", agentPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("user cannot access file")
	}
	return nil
}

// CheckAccessibleBySystemd tests if agent is accessible within systemd security context
// Moved from main.go: isAgentAccessibleBySystemd()
func CheckAccessibleBySystemd(agentPath string, username string) error {
	cmd := exec.Command("systemd-run",
		"--property=User="+username,
		"--wait",
		"--quiet",
		"--service-type=oneshot",
		"test", "-r", agentPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemd security context blocks access, ")
	}
	return nil
}

// ValidateAccessForUsers validates agent access for multiple users
// Moved from main.go: ensureAgentAccessibleForAll()
func ValidateAccessForUsers(agentPath string, processes []discovery.JavaProcess) (string, error) {
	// Check if file exists
	if _, err := os.Stat(agentPath); err != nil {
		return "", fmt.Errorf("agent file does not exist: %s", agentPath)
	}

	if !strings.HasSuffix(agentPath, ".jar") {
		return "", fmt.Errorf("agent file must be a .jar file: %s", agentPath)
	}

	// Collect all unique users
	users := make(map[string]bool)
	for _, proc := range processes {
		users[proc.ProcessOwner] = true
	}

	// Check accessibility for each user
	inaccessibleUsers := []string{}
	for user := range users {
		if err := CheckAccessibleByUser(agentPath, user); err != nil {
			inaccessibleUsers = append(inaccessibleUsers, user)
		}
	}

	// If all users can access, we're done
	if len(inaccessibleUsers) == 0 {
		fmt.Printf("[ok] Agent is accessible by all service users\n")
		return agentPath, nil
	}

	// Show warning
	fmt.Printf("\n[warn]  Warning: The following users cannot access %s:\n", agentPath)
	for _, user := range inaccessibleUsers {
		fmt.Printf("   - %s\n", user)
	}
	fmt.Print("\n   Copy agent to /opt/middleware/agents/ with proper permissions? [Y/n]: ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response == "n" || response == "no" {
		return "", fmt.Errorf("agent not accessible and user declined to copy")
	}

	// Copy to shared location
	sharedDir := "/opt/middleware/agents"
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create shared directory: %v", err)
	}

	agentName := filepath.Base(agentPath)
	newPath := filepath.Join(sharedDir, agentName)

	data, err := os.ReadFile(agentPath)
	if err != nil {
		return "", fmt.Errorf("failed to read agent file: %v", err)
	}

	if err := os.WriteFile(newPath, data, 0o644); err != nil {
		return "", fmt.Errorf("failed to write agent file: %v", err)
	}

	fmt.Printf("   [ok] Copied agent to: %s\n", newPath)
	fmt.Printf("   Permissions: -rw-r--r-- (readable by all users)\n")

	return newPath, nil
}

// EnsureAccessibleForSingle ensures agent is accessible for a single process
// Moved from main.go: ensureAgentAccessible()
func EnsureAccessibleForSingle(agentPath string, proc *discovery.JavaProcess) (string, error) {
	// First check if it's already accessible
	if err := ValidateAgentPathForUser(agentPath, proc.ProcessOwner); err == nil {
		return agentPath, nil
	}

	// If not accessible, offer to copy to shared location
	fmt.Printf("\n[warn]  Warning: User '%s' cannot access %s\n", proc.ProcessOwner, agentPath)
	fmt.Print("   Copy agent to /opt/middleware/agents/? [Y/n]: ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response == "n" || response == "no" {
		return "", fmt.Errorf("agent not accessible and user declined to copy")
	}

	// Create shared directory
	sharedDir := "/opt/middleware/agents"
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create shared directory: %v", err)
	}

	// Copy the agent
	agentName := filepath.Base(agentPath)
	newPath := filepath.Join(sharedDir, agentName)

	// Read source file
	data, err := os.ReadFile(agentPath)
	if err != nil {
		return "", fmt.Errorf("failed to read agent file: %v", err)
	}

	// Write to new location
	if err := os.WriteFile(newPath, data, 0o644); err != nil {
		return "", fmt.Errorf("failed to write agent file: %v", err)
	}

	// Set appropriate permissions
	if err := os.Chmod(newPath, 0o644); err != nil {
		return "", fmt.Errorf("failed to set permissions: %v", err)
	}

	fmt.Printf("   [ok] Copied agent to: %s\n", newPath)

	// Verify the new path is accessible
	if err := CheckAccessibleByUser(newPath, proc.ProcessOwner); err != nil {
		return "", fmt.Errorf("copied agent still not accessible: %v", err)
	}

	return newPath, nil
}

// TODO: Add custom error types for better error handling
// - AgentNotFoundError
// - AgentPermissionError
// - AgentValidationError
