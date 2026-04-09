package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// EnsureInstalled checks if the agent exists and is properly configured
// Moved from main.go: EnsureAgentInstalled()
func EnsureInstalled(sourcePath string, targetPath string) (string, error) {
	// If source path is already the target location, just validate permissions
	if sourcePath == targetPath {
		return targetPath, ValidatePermissions(targetPath)
	}

	// Check if target agent already exists
	if _, err := os.Stat(targetPath); err == nil {
		fmt.Printf("Agent already exists at %s\n", targetPath)
		return targetPath, ValidatePermissions(targetPath)
	}

	// Create directory structure
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create agent directory: %w", err)
	}

	// Copy agent to target location
	if err := copyAgentFile(sourcePath, targetPath); err != nil {
		return "", fmt.Errorf("failed to copy agent: %w", err)
	}

	// Set proper permissions
	if err := os.Chmod(targetPath, 0o644); err != nil {
		return "", fmt.Errorf("failed to set agent permissions: %w", err)
	}

	// Set ownership to root:root
	if err := os.Chown(targetPath, 0, 0); err != nil {
		return "", fmt.Errorf("failed to set agent ownership: %w", err)
	}

	fmt.Printf("[ok] Agent installed to %s with proper permissions\n", targetPath)
	return targetPath, nil
}

// ValidatePermissions ensures the agent JAR has correct permissions
// Moved from main.go: ValidateAgentPermissions()
func ValidatePermissions(agentPath string) error {
	info, err := os.Stat(agentPath)
	if err != nil {
		return fmt.Errorf("agent not found: %w", err)
	}

	// Check if world-readable
	mode := info.Mode()
	if mode&0o004 == 0 {
		fmt.Printf("[warn]  Warning: Agent is not world-readable\n")
		fmt.Printf("   Current permissions: %s\n", mode)
		fmt.Printf("   Fixing permissions...\n")

		if err := os.Chmod(agentPath, 0o644); err != nil {
			return fmt.Errorf("failed to fix permissions: %w", err)
		}
		fmt.Printf("[ok] Permissions fixed to 0644\n")
	}

	return nil
}

// copyAgentFile copies the agent JAR from source to destination
// Moved from main.go: copyAgent()
func copyAgentFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

// IsAccessible tests if a user can access the agent
// Moved from main.go: CheckAgentAccessible()
func IsAccessible(agentPath, username string) bool {
	// This is a simplified check - for production, you'd want to
	// actually test with the specific user's permissions
	info, err := os.Stat(agentPath)
	if err != nil {
		return false
	}

	// Check if world-readable (0004 bit)
	return info.Mode()&0o004 != 0
}

// Install performs agent installation with custom configuration
func Install(config *InstallationConfig) error {
	// Create target directory
	targetDir := filepath.Dir(config.TargetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Copy agent file
	if err := copyAgentFile(config.SourcePath, config.TargetPath); err != nil {
		return fmt.Errorf("failed to copy agent: %w", err)
	}

	// Set permissions
	if err := os.Chmod(config.TargetPath, config.RequiredPerms); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Set ownership
	if err := os.Chown(config.TargetPath, config.OwnerUID, config.OwnerGID); err != nil {
		return fmt.Errorf("failed to set ownership: %w", err)
	}

	return nil
}

// TODO: Add functions for:
// - Agent version detection from JAR manifest
// - Agent signature validation
// - Automatic agent updates
