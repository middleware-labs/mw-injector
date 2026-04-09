package discovery

import (
	"fmt"
	"time"
)

// PythonProcess represents a discovered Python process with OTEL semantic convention compliance
type PythonProcess struct {
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
	ProcessRuntimeName        string `json:"process.runtime.name"` // cpython, pypy
	ProcessRuntimeVersion     string `json:"process.runtime.version"`
	ProcessRuntimeDescription string `json:"process.runtime.description"`

	// Python-specific information
	PythonVersion    string `json:"python.version,omitempty"`
	EntryPoint       string `json:"python.entry_point,omitempty"` // app.py, main.py
	ModulePath       string `json:"python.module_path,omitempty"` // -m my_app
	WorkingDirectory string `json:"python.working_directory,omitempty"`
	VirtualEnvPath   string `json:"python.venv_path,omitempty"`

	// Process manager detection
	IsGunicornProcess bool   `json:"python.gunicorn_process"`
	IsUvicornProcess  bool   `json:"python.uvicorn_process"`
	IsCeleryProcess   bool   `json:"python.celery_process"`
	ProcessManager    string `json:"python.process_manager,omitempty"` // gunicorn, supervisor, systemd

	// Instrumentation detection
	HasPythonAgent    bool            `json:"python.agent.present"`
	PythonAgentPath   string          `json:"python.agent.path,omitempty"`
	PythonAgentType   PythonAgentType `json:"python.agent.type"`
	IsMiddlewareAgent bool            `json:"middleware.agent.detected"`

	// Service identification
	ServiceName string `json:"service.name,omitempty"`

	// Process metrics
	MemoryPercent float32 `json:"process.memory.percent"`
	CPUPercent    float64 `json:"process.cpu.percent"`
	Status        string  `json:"process.status"`

	// Container information
	ContainerInfo *ContainerInfo `json:"container_info,omitempty"`
}

// PythonAgentType represents the type of Python agent detected
type PythonAgentType int

const (
	PythonAgentNone          PythonAgentType = iota
	PythonAgentOpenTelemetry                  // opentelemetry-instrument wrapper
	PythonAgentMiddleware                     // mw_bootstrap injected into PYTHONPATH
	PythonAgentOtelInjector                   // LD_PRELOAD + libotelinject.so drop-in
	PythonAgentOther
)

func (a PythonAgentType) String() string {
	switch a {
	case PythonAgentNone:
		return "none"
	case PythonAgentOpenTelemetry:
		return "opentelemetry"
	case PythonAgentMiddleware:
		return "middleware"
	case PythonAgentOtelInjector:
		return "otel-injector"
	case PythonAgentOther:
		return "other"
	default:
		return "unknown"
	}
}

// Methods for PythonProcess

func (pp *PythonProcess) IsInContainer() bool {
	return pp.ContainerInfo != nil && pp.ContainerInfo.IsContainer
}

func (pp *PythonProcess) HasInstrumentation() bool {
	return pp.HasPythonAgent
}

// FormatAgentStatus returns a human-readable agent status string
func (pp *PythonProcess) FormatAgentStatus() string {
	status := "[x] None"
	if pp.HasPythonAgent {
		if pp.IsMiddlewareAgent {
			status = "[ok] MW"
		} else {
			status = "[ok] OTel"
		}
	}

	if pp.IsInContainer() {
		status += fmt.Sprintf(" (container: %s)", pp.ContainerInfo.Runtime)
	}

	if pp.IsGunicornProcess {
		status += " (Gunicorn)"
	}
	return status
}

