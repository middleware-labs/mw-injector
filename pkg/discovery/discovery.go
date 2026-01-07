// Package discovery provides Java process discovery and instrumentation detection
// capabilities following OpenTelemetry semantic conventions.
package discovery

import (
	"context"
	"sync"
	"time"
)

// JavaProcess represents a discovered Java process with OTEL semantic convention compliance
type JavaProcess struct {
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

	// Java-specific information
	JarFile    string   `json:"java.jar.file,omitempty"`
	JarPath    string   `json:"java.jar.path,omitempty"`
	MainClass  string   `json:"java.main.class,omitempty"`
	JVMOptions []string `json:"java.jvm.options,omitempty"`

	// Instrumentation detection
	HasJavaAgent      bool   `json:"java.agent.present"`
	JavaAgentPath     string `json:"java.agent.path,omitempty"`
	JavaAgentName     string `json:"java.agent.name,omitempty"`
	IsMiddlewareAgent bool   `json:"middleware.agent.detected"`

	// Service identification
	ServiceName string `json:"service.name,omitempty"`

	// Process metrics
	MemoryPercent float32 `json:"process.memory.percent"`
	CPUPercent    float64 `json:"process.cpu.percent"`
	Status        string  `json:"process.status"`

	ContainerInfo *ContainerInfo `json:"container_info,omitempty"`
}

// 2. Add these methods to JavaProcess (can go at the end of discovery.go)
func (jp *JavaProcess) IsInContainer() bool {
	return jp.ContainerInfo != nil && jp.ContainerInfo.IsContainer
}

func (jp *JavaProcess) GetContainerRuntime() string {
	if jp.ContainerInfo != nil {
		return jp.ContainerInfo.Runtime
	}
	return ""
}

func (jp *JavaProcess) GetContainerID() string {
	if jp.ContainerInfo != nil {
		return jp.ContainerInfo.ContainerID
	}
	return ""
}

func (jp *JavaProcess) GetContainerName() string {
	if jp.ContainerInfo != nil {
		return jp.ContainerInfo.ContainerName
	}
	return ""
}

// Discoverer defines the main interface for Java process discovery
type Discoverer interface {
	// DiscoverJavaProcesses finds all Java processes with default options
	DiscoverJavaProcesses(ctx context.Context) ([]JavaProcess, error)

	// DiscoverWithOptions finds Java processes with custom configuration
	DiscoverWithOptions(ctx context.Context, opts DiscoveryOptions) ([]JavaProcess, error)

	// RefreshProcess updates information for a specific process
	RefreshProcess(ctx context.Context, pid int32) (*JavaProcess, error)

	// Close cleans up any resources used by the discoverer
	Close() error
}

// DiscoveryOptions configures the discovery behavior
type DiscoveryOptions struct {
	// MaxConcurrency controls the number of goroutines for concurrent processing
	MaxConcurrency int `json:"max_concurrency"`

	// Timeout sets the maximum time for the entire discovery operation
	Timeout time.Duration `json:"timeout"`

	// SkipPermissionErrors continues discovery even when access is denied
	SkipPermissionErrors bool `json:"skip_permission_errors"`

	// IncludeEnvironment includes environment variables in the discovery
	IncludeEnvironment bool `json:"include_environment"`

	// IncludeMetrics includes CPU and memory metrics
	IncludeMetrics bool `json:"include_metrics"`

	// Filter specifies which processes to include/exclude
	Filter ProcessFilter `json:"filter"`

	// ExcludeContainers excludes processes running inside containers
	ExcludeContainers bool `json:"exclude_containers"`

	// IncludeContainerInfo includes container information for all processes
	IncludeContainerInfo bool `json:"include_container_info"`
}

// ProcessFilter defines filtering criteria for process discovery
type ProcessFilter struct {
	// IncludeUsers limits discovery to processes owned by these users
	IncludeUsers []string `json:"include_users,omitempty"`

	// ExcludeUsers excludes processes owned by these users
	ExcludeUsers []string `json:"exclude_users,omitempty"`

	// CurrentUserOnly limits discovery to processes owned by the current user
	CurrentUserOnly bool `json:"current_user_only"`

	// HasJavaAgentOnly includes only processes with Java agents
	HasJavaAgentOnly bool `json:"has_java_agent_only"`

	// HasMWAgentOnly includes only processes with Middleware agents
	HasMWAgentOnly bool `json:"has_mw_agent_only"`

	// ServiceNamePattern includes only services matching this pattern
	ServiceNamePattern string `json:"service_name_pattern,omitempty"`

	// MinMemoryMB includes only processes using at least this much memory
	MinMemoryMB float32 `json:"min_memory_mb,omitempty"`
}

// AgentType represents the type of Java agent detected
type AgentType int

const (
	AgentNone AgentType = iota
	AgentOpenTelemetry
	AgentMiddleware
	AgentOther
)

// AgentInfo contains details about a detected Java agent
type AgentInfo struct {
	Type         AgentType `json:"type"`
	Path         string    `json:"path"`
	Name         string    `json:"name"`
	Version      string    `json:"version,omitempty"`
	IsServerless bool      `json:"is_serverless,omitempty"`
}

// String returns a human-readable representation of the agent type
func (a AgentType) String() string {
	switch a {
	case AgentNone:
		return "none"
	case AgentOpenTelemetry:
		return "opentelemetry"
	case AgentMiddleware:
		return "middleware"
	case AgentOther:
		return "other"
	default:
		return "unknown"
	}
}

// DefaultDiscoveryOptions returns sensible defaults for process discovery
func DefaultDiscoveryOptions() DiscoveryOptions {
	return DiscoveryOptions{
		MaxConcurrency:       10,
		Timeout:              30 * time.Second,
		SkipPermissionErrors: true,
		IncludeEnvironment:   false,
		IncludeMetrics:       true,
		Filter:               ProcessFilter{},
	}
}

// NewDiscoverer creates a new process discoverer with default options
func NewDiscoverer(ctx context.Context, opts DiscoveryOptions) *discoverer {
	return NewDiscovererWithOptions(ctx, DefaultDiscoveryOptions())
}

// NewDiscovererWithOptions creates a new process discoverer with custom options
func NewDiscovererWithOptions(ctx context.Context, opts DiscoveryOptions) *discoverer {
	return &discoverer{
		ctx:               ctx,
		opts:              opts,
		containerDetector: NewContainerDetector(),
		userCache:         sync.Map{},
	}
}

// Convenience functions for common use cases

// FindAllJavaProcesses finds all Java processes, excluding those in containers
func FindAllJavaProcesses(ctx context.Context) ([]JavaProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true

	discoverer := NewDiscoverer(ctx, opts)
	defer discoverer.Close()

	return discoverer.DiscoverWithOptions(ctx, opts)
}

func FindAllNodeProcesses(ctx context.Context) ([]NodeProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = true
	opts.IncludeContainerInfo = true

	discoverer := NewDiscoverer(ctx, opts)
	defer discoverer.Close()

	return discoverer.DiscoverNodeWithOptions(ctx, opts)
}

func FindAllPythonProcess(ctx context.Context) ([]PythonProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true

	discoverer := NewDiscoverer(ctx, opts)
	defer discoverer.Close()

	return discoverer.DiscoverPythonWithOptions(ctx, opts)
}

// FindCurrentUserJavaProcesses discovers Java processes for the current user only
func FindCurrentUserJavaProcesses(ctx context.Context) ([]JavaProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.Filter.CurrentUserOnly = true

	d := NewDiscovererWithOptions(ctx, opts)
	defer d.Close()
	return d.DiscoverWithOptions(ctx, opts)
}

// FindInstrumentedProcesses discovers only Java processes with agents
func FindInstrumentedProcesses(ctx context.Context) ([]JavaProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.Filter.HasJavaAgentOnly = true

	d := NewDiscovererWithOptions(ctx, opts)
	defer d.Close()
	return d.DiscoverWithOptions(ctx, opts)
}

// FindMiddlewareProcesses discovers only processes with Middleware agents
func FindMiddlewareProcesses(ctx context.Context) ([]JavaProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.Filter.HasMWAgentOnly = true

	d := NewDiscovererWithOptions(ctx, opts)
	defer d.Close()
	return d.DiscoverWithOptions(ctx, opts)
}
