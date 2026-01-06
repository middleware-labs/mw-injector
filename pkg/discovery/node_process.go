package discovery

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// NodeProcess represents a discovered Node.js process with OTEL semantic convention compliance
type NodeProcess struct {
	// OTEL Process semantic conventions
	ProcessPID            int32     `json:"process.pid"`
	ProcessParentPID      int32     `json:"process.parent_pid"`
	ProcessExecutableName string    `json:"process.executable.name"`
	ProcessExecutablePath string    `json:"process.executable.path"`
	ProcessCommand        string    `json:"process.command"`
	ProcessCommandLine    string    `json:"process.command_line"`
	ProcessCommandArgs    []string  `json:"process.command_args"`
	ProcessOwner          string    `json:"process.owner"`
	ProcessCreateTime     time.Time `json:"process.create_time"`

	// OTEL Process Runtime semantic conventions
	ProcessRuntimeName        string `json:"process.runtime.name"`
	ProcessRuntimeVersion     string `json:"process.runtime.version"`
	ProcessRuntimeDescription string `json:"process.runtime.description"`

	// Node.js-specific information
	NodeVersion      string   `json:"node.version,omitempty"`
	EntryPoint       string   `json:"node.entry_point,omitempty"` // app.js, index.js, etc.
	WorkingDirectory string   `json:"node.working_directory,omitempty"`
	PackageJsonPath  string   `json:"node.package_json_path,omitempty"`
	PackageName      string   `json:"node.package_name,omitempty"`
	PackageVersion   string   `json:"node.package_version,omitempty"`
	Dependencies     []string `json:"node.dependencies,omitempty"`

	// Process manager detection
	IsPM2Process     bool   `json:"node.pm2_process"`
	PM2ProcessName   string `json:"node.pm2_name,omitempty"`
	IsForeverProcess bool   `json:"node.forever_process"`
	ProcessManager   string `json:"node.process_manager,omitempty"` // pm2, forever, systemd

	// Instrumentation detection
	HasNodeAgent      bool   `json:"node.agent.present"`
	NodeAgentPath     string `json:"node.agent.path,omitempty"`
	IsMiddlewareAgent bool   `json:"middleware.agent.detected"`

	// Service identification
	ServiceName string `json:"service.name,omitempty"`

	// Process metrics
	MemoryPercent float32 `json:"process.memory.percent"`
	CPUPercent    float64 `json:"process.cpu.percent"`
	Status        string  `json:"process.status"`

	// Container information
	ContainerInfo *ContainerInfo `json:"container_info,omitempty"`
}

// NodeAgentInfo contains details about a detected Node.js agent
type NodeAgentInfo struct {
	Type         NodeAgentType `json:"type"`
	Path         string        `json:"path"`
	Name         string        `json:"name"`
	Version      string        `json:"version,omitempty"`
	IsServerless bool          `json:"is_serverless,omitempty"`
}

// NodeAgentType represents the type of Node.js agent detected
type NodeAgentType int

const (
	NodeAgentNone NodeAgentType = iota
	NodeAgentOpenTelemetry
	NodeAgentMiddleware
	NodeAgentOther
)

// String returns a human-readable representation of the Node.js agent type
func (a NodeAgentType) String() string {
	switch a {
	case NodeAgentNone:
		return "none"
	case NodeAgentOpenTelemetry:
		return "opentelemetry"
	case NodeAgentMiddleware:
		return "middleware"
	case NodeAgentOther:
		return "other"
	default:
		return "unknown"
	}
}

// PackageInfo represents package.json information
type PackageInfo struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Description     string            `json:"description"`
	Main            string            `json:"main"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Keywords        []string          `json:"keywords"`
}

// Methods for NodeProcess

// IsInContainer checks if the Node.js process is running in a container
func (np *NodeProcess) IsInContainer() bool {
	return np.ContainerInfo != nil && np.ContainerInfo.IsContainer
}

// GetContainerRuntime returns the container runtime if running in a container
func (np *NodeProcess) GetContainerRuntime() string {
	if np.ContainerInfo != nil {
		return np.ContainerInfo.Runtime
	}
	return ""
}

// GetContainerID returns the container ID if running in a container
func (np *NodeProcess) GetContainerID() string {
	if np.ContainerInfo != nil {
		return np.ContainerInfo.ContainerID
	}
	return ""
}

// GetContainerName returns the container name if running in a container
func (np *NodeProcess) GetContainerName() string {
	if np.ContainerInfo != nil {
		return np.ContainerInfo.ContainerName
	}
	return ""
}

// HasInstrumentation checks if the process has any form of instrumentation
func (np *NodeProcess) HasInstrumentation() bool {
	return np.HasNodeAgent
}

// HasMiddlewareInstrumentation checks specifically for Middleware instrumentation
func (np *NodeProcess) HasMiddlewareInstrumentation() bool {
	return np.IsMiddlewareAgent
}

// IsSystemdProcess checks if this is likely a systemd-managed process
func (np *NodeProcess) IsSystemdProcess() bool {
	return np.ProcessManager == "systemd" ||
		(np.ProcessOwner != "root" && np.ProcessOwner != "node" && !np.IsPM2Process && !np.IsForeverProcess)
}

// FormatAgentStatus returns a human-readable agent status string
func (np *NodeProcess) FormatAgentStatus() string {
	var status string

	if !np.HasNodeAgent {
		status = "❌ None"
	} else {
		agentInfo := np.GetAgentInfo()
		switch agentInfo.Type {
		case NodeAgentMiddleware:
			if agentInfo.IsServerless {
				status = "✅ MW (Serverless)"
			} else {
				status = "✅ MW"
			}
		case NodeAgentOpenTelemetry:
			status = "✅ OTel"
		case NodeAgentOther:
			status = "✅ Other"
		default:
			status = "⚠️ Unknown"
		}
	}

	// Add container indicator
	if np.IsInContainer() {
		status += fmt.Sprintf(" (📦 %s)", np.GetContainerRuntime())
	}

	// Add process manager indicator
	if np.IsPM2Process {
		status += " (PM2)"
	} else if np.IsForeverProcess {
		status += " (Forever)"
	}

	return status
}

// GetAgentInfo returns detailed information about the detected agent
func (np *NodeProcess) GetAgentInfo() *NodeAgentInfo {
	if !np.HasNodeAgent {
		return &NodeAgentInfo{
			Type: NodeAgentNone,
		}
	}

	agentType := NodeAgentOther
	version := "unknown"
	isServerless := false

	// Determine agent type
	if np.IsMiddlewareAgent {
		agentType = NodeAgentMiddleware
		// TODO: Extract version and serverless detection for Middleware agents
	} else {
		// Check for other agent types based on path/name patterns
		agentPath := strings.ToLower(np.NodeAgentPath)
		if strings.Contains(agentPath, "opentelemetry") || strings.Contains(agentPath, "otel") {
			agentType = NodeAgentOpenTelemetry
		}
	}

	return &NodeAgentInfo{
		Type:         agentType,
		Path:         np.NodeAgentPath,
		Name:         filepath.Base(np.NodeAgentPath),
		Version:      version,
		IsServerless: isServerless,
	}
}
