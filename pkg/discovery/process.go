// process.go defines the unified Process struct that represents a discovered
// process of any supported language. Common fields follow OpenTelemetry
// semantic conventions; language-specific metadata lives in the Details map.
package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// Detail key constants for language-specific metadata stored in Process.Details.
// Each language handler populates the keys relevant to its language.
const (
	// Java detail keys
	DetailJarFile    = "jar.file"
	DetailJarPath    = "jar.path"
	DetailMainClass  = "main.class"
	DetailJVMOptions = "jvm.options"
	DetailIsTomcat   = "is_tomcat"

	// Node.js detail keys
	DetailNodeVersion      = "node.version"
	DetailEntryPoint       = "entry_point"
	DetailWorkingDirectory = "working_directory"
	DetailPackageJsonPath  = "package_json_path"
	DetailPackageName      = "package_name"
	DetailPackageVersion   = "package_version"
	DetailDependencies     = "dependencies"
	DetailIsPM2            = "is_pm2"
	DetailPM2Name          = "pm2_name"
	DetailIsForever        = "is_forever"

	// Go detail keys
	DetailGoModule = "go.module"

	// Python detail keys
	DetailModulePath    = "module_path"
	DetailVenvPath      = "venv_path"
	DetailIsGunicorn    = "is_gunicorn"
	DetailIsUvicorn     = "is_uvicorn"
	DetailIsCelery      = "is_celery"
	DetailPythonVersion = "python_version"

	// Common detail keys shared across languages
	DetailProcessManager      = "process_manager"
	DetailListeners           = "listeners" // []Listener — TCP/UDP sockets the process is listening on
	DetailSystemdUnit         = "systemd_unit"
	DetailExplicitServiceName = "explicit_service_name"
)

// Process represents a discovered process of any supported language.
// Common OTEL semantic convention fields are top-level struct fields.
// Language-specific metadata lives in the Details map, populated by
// each LanguageHandler during the enrichment phase.
type Process struct {
	// OTEL Process semantic conventions
	PID            int32     `json:"process.pid"`
	ParentPID      int32     `json:"process.parent_pid"`
	ExecutableName string    `json:"process.executable.name"`
	ExecutablePath string    `json:"process.executable.path"`
	Command        string    `json:"process.command"`
	CommandLine    string    `json:"process.command_line"`
	CommandArgs    []string  `json:"process.command_args"`
	Owner          string    `json:"process.owner"`
	CreateTime     time.Time `json:"process.create_time"`

	// OTEL Process Runtime semantic conventions
	RuntimeName        string `json:"process.runtime.name"`
	RuntimeVersion     string `json:"process.runtime.version"`
	RuntimeDescription string `json:"process.runtime.description"`

	// Language classification — set by the handler that enriched this process.
	Language Language `json:"language"`

	// Unified agent detection — normalized across all languages.
	HasAgent          bool   `json:"agent.present"`
	AgentPath         string `json:"agent.path,omitempty"`
	AgentName         string `json:"agent.name,omitempty"`
	AgentType         string `json:"agent.type,omitempty"` // "middleware", "opentelemetry", "otel-injector", etc.
	IsMiddlewareAgent bool   `json:"middleware.agent.detected"`

	// Service identification
	ServiceName string `json:"service.name,omitempty"`

	// Process metrics
	MemoryPercent float32 `json:"process.memory.percent"`
	CPUPercent    float64 `json:"process.cpu.percent"`
	Status        string  `json:"process.status"`

	// Container information
	ContainerInfo *ContainerInfo `json:"container_info,omitempty"`

	// Language-specific metadata. Each handler populates the keys it needs
	// during enrichment. Use the Detail* helper methods for typed access.
	Details map[string]any `json:"details,omitempty"`
}

// IsInContainer returns true if this process is running inside a container.
func (p *Process) IsInContainer() bool {
	return p.ContainerInfo != nil && p.ContainerInfo.IsContainer
}

// GetContainerRuntime returns the container runtime name (docker, podman, etc.)
// or an empty string if the process is not in a container.
func (p *Process) GetContainerRuntime() string {
	if p.ContainerInfo != nil {
		return p.ContainerInfo.Runtime
	}
	return ""
}

// GetContainerID returns the container ID or an empty string.
func (p *Process) GetContainerID() string {
	if p.ContainerInfo != nil {
		return p.ContainerInfo.ContainerID
	}
	return ""
}

// GetContainerName returns the container name or an empty string.
func (p *Process) GetContainerName() string {
	if p.ContainerInfo != nil {
		return p.ContainerInfo.ContainerName
	}
	return ""
}

// Fingerprint returns a stable identity hash for this process that represents
// workload class identity (not instance identity). It uses an additive formula:
// all available signals are hashed together so no single missing field can
// cause a collision. Version-agnostic: runtime upgrades and jar version bumps
// do not change the fingerprint. Ports and PIDs are excluded.
func (p *Process) Fingerprint() string {
	parts := []string{string(p.Language)}

	if unit := p.DetailString(DetailSystemdUnit); unit != "" {
		parts = append(parts, unit)
	}
	if p.ContainerInfo != nil && p.ContainerInfo.ContainerName != "" {
		parts = append(parts, p.ContainerInfo.ContainerName)
	}

	switch p.Language {
	case LangJava:
		if jar := p.DetailString(DetailJarFile); jar != "" {
			parts = append(parts, stripJarVersion(jar))
		}
		if mc := p.DetailString(DetailMainClass); mc != "" {
			parts = append(parts, mc)
		}
	case LangNode:
		if pkg := p.DetailString(DetailPackageName); pkg != "" && pkg != "unknown" {
			parts = append(parts, pkg)
		}
		if ep := p.DetailString(DetailEntryPoint); ep != "" {
			parts = append(parts, ep)
		}
	case LangPython:
		if mp := p.DetailString(DetailModulePath); mp != "" {
			parts = append(parts, mp)
		}
		if ep := p.DetailString(DetailEntryPoint); ep != "" {
			parts = append(parts, ep)
		}
	case LangGo:
		if mod := p.DetailString(DetailGoModule); mod != "" {
			parts = append(parts, mod)
		}
		parts = append(parts, p.ExecutablePath)
	case LangRust:
		if ep := p.DetailString(DetailEntryPoint); ep != "" {
			parts = append(parts, ep)
		}
	case LangRuby:
		if ep := p.DetailString(DetailEntryPoint); ep != "" {
			parts = append(parts, ep)
		}
		parts = append(parts, p.ExecutablePath)
	}

	if cwd := p.DetailString(DetailWorkingDirectory); cwd != "" {
		parts = append(parts, cwd)
	}

	sort.Strings(parts[1:])
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:8])
}

// DetailString returns the string value for a detail key, or "" if missing or wrong type.
func (p *Process) DetailString(key string) string {
	if p.Details == nil {
		return ""
	}
	v, ok := p.Details[key].(string)
	if !ok {
		return ""
	}
	return v
}

// DetailBool returns the bool value for a detail key, or false if missing or wrong type.
func (p *Process) DetailBool(key string) bool {
	if p.Details == nil {
		return false
	}
	v, ok := p.Details[key].(bool)
	if !ok {
		return false
	}
	return v
}

// Listeners returns the TCP/UDP listening sockets detected for this process,
// or nil if none were found or detection was skipped.
func (p *Process) Listeners() []Listener {
	if p.Details == nil {
		return nil
	}
	v, ok := p.Details[DetailListeners].([]Listener)
	if !ok {
		return nil
	}
	return v
}

// DetailStringSlice returns the []string value for a detail key, or nil if missing.
func (p *Process) DetailStringSlice(key string) []string {
	if p.Details == nil {
		return nil
	}
	v, ok := p.Details[key].([]string)
	if !ok {
		return nil
	}
	return v
}
