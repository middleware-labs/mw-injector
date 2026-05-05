package state

import "time"

// OrphanedConfig represents a configuration file for a stopped/crashed service
type OrphanedConfig struct {
	ConfigPath  string `json:"config_path"`
	ServiceName string `json:"service_name"`
	IsTomcat    bool   `json:"is_tomcat"`
}

// HostState represents the state of host-based instrumentation
type HostState struct {
	OrphanedConfigs []OrphanedConfig `json:"orphaned_configs"`
	LastScan        time.Time        `json:"last_scan"`
	Version         string           `json:"version"`
}

// StateFile represents a generic state file structure
type StateFile struct {
	Path         string
	RequiredDirs []string
}

// ValidationResult represents the result of state validation
type ValidationResult struct {
	IsValid  bool     `json:"is_valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

const (
	// DefaultStateDir is the centralized state directory
	DefaultStateDir = "/etc/middleware/state"

	// HostStateFile is the file for host instrumentation state
	HostStateFile = "host.json"

	// StateVersion is the current state format version
	StateVersion = "1.0"
)
