// python_handler.go implements the LanguageHandler interface for Python processes.
// It handles detection of CPython/PyPy processes and Python-based binaries
// (gunicorn, uvicorn, celery), enrichment with module/entry point info,
// process manager details, and instrumentation state. Also contains Python-specific
// helper functions and agent type definitions.
package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PythonHandler implements LanguageHandler for Python processes.
// It detects CPython/PyPy processes and Python-based binaries (gunicorn,
// uvicorn, celery), enriches them with module/entry point info, process
// manager details, and instrumentation state.
type PythonHandler struct{}

// Lang returns LangPython.
func (h *PythonHandler) Lang() Language { return LangPython }

// Detect returns true if the process is a Python process, identified by
// executable name (python, python3, pypy), Python-based binaries
// (gunicorn, uvicorn, celery, flask), or .py file references in cmdline.
func (h *PythonHandler) Detect(proc *ProcessInfo) bool {
	exeLower := strings.ToLower(proc.ExeName)

	if pythonExecutables[exeLower] || strings.HasPrefix(exeLower, "python3.") {
		return true
	}

	if pythonBinaries[exeLower] {
		return true
	}

	cmdLower := strings.ToLower(proc.CmdLine)
	for _, pattern := range pythonCmdPatterns {
		if strings.Contains(cmdLower, pattern) {
			return true
		}
	}

	// Fallback: any .py file reference in cmdline
	if strings.Contains(cmdLower, ".py") {
		return true
	}

	return false
}

// Enrich populates a Process struct with Python-specific details.
// Returns nil if the process should be skipped.
func (h *PythonHandler) Enrich(info *ProcessInfo, opts DiscoveryOptions, detector *ContainerDetector) *Process {
	pid := info.PID
	cmdArgs := info.CmdArgs

	owner := readProcessOwner(pid)
	createTime := readProcessCreateTime(pid)
	alignedTime := (createTime / 1000) * 1000

	// Cache fast path
	if cached, hit := GetCachedProcessMetadata(pid, alignedTime); hit {
		if cached.Ignore {
			return nil
		}

		status := readProcessStatus(pid)

		return &Process{
			PID:            pid,
			ParentPID:      readProcessPPID(pid),
			ExecutableName: info.ExeName,
			ExecutablePath: info.ExePath,
			CommandLine:    info.CmdLine,
			Owner:          cached.Owner,
			CreateTime:     timeFromMillis(createTime),
			Status:         status,
			Language:       LangPython,

			ServiceName:        cached.ServiceName,
			RuntimeName:        "python",
			RuntimeVersion:     cached.RuntimeVersion,
			RuntimeDescription: "Python Interpreter",

			HasAgent:          cached.HasAgent,
			IsMiddlewareAgent: cached.IsMiddlewareAgent,
			AgentPath:         cached.AgentPath,
			AgentType:         cached.AgentType,
			ContainerInfo:     cached.ContainerInfo,

			Details: map[string]any{
				DetailEntryPoint:     cached.EntryPoint,
				DetailProcessManager: cached.ServiceType,
				DetailIsGunicorn:     cached.ServiceType == "gunicorn",
				DetailIsUvicorn:      cached.ServiceType == "uvicorn",
				DetailIsCelery:       cached.ServiceType == "celery",
			},
		}
	}

	// Slow path
	if isIgnoredSystemdUnit(pid) {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true})
		return nil
	}

	status := readProcessStatus(pid)

	proc := &Process{
		PID:            pid,
		ParentPID:      readProcessPPID(pid),
		ExecutableName: info.ExeName,
		ExecutablePath: info.ExePath,
		CommandLine:    info.CmdLine,
		CommandArgs:    cmdArgs,
		Owner:          owner,
		CreateTime:     timeFromMillis(createTime),
		Status:         status,
		Language:       LangPython,

		RuntimeName:        "python",
		RuntimeDescription: "Python Interpreter",

		Details: make(map[string]any),
	}

	// Container detection
	if opts.IncludeContainerInfo {
		containerInfo, err := detector.IsProcessInContainer(pid)
		if err == nil && containerInfo.IsContainer {
			proc.ContainerInfo = containerInfo
		}
	}

	// Skip multiprocessing sub-processes
	isSubProcess := strings.Contains(info.CmdLine, "multiprocessing.spawn") ||
		strings.Contains(info.CmdLine, "resource_tracker")
	if isSubProcess {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true, Owner: owner})
		return nil
	}

	h.extractPythonInfo(proc, cmdArgs)
	h.extractServiceName(proc, cmdArgs)
	h.detectProcessManager(proc, cmdArgs)
	h.detectInstrumentation(proc, cmdArgs)

	// Populate cache
	CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{
		ServiceName:       proc.ServiceName,
		ServiceType:       proc.DetailString(DetailProcessManager),
		RuntimeVersion:    proc.RuntimeVersion,
		EntryPoint:        proc.DetailString(DetailEntryPoint),
		HasAgent:          proc.HasAgent,
		IsMiddlewareAgent: proc.IsMiddlewareAgent,
		AgentPath:         proc.AgentPath,
		AgentType:         proc.AgentType,
		ContainerInfo:     proc.ContainerInfo,
		Owner:             proc.Owner,
	})

	return proc
}

// PassesFilter checks simple owner-based filtering for Python processes.
func (h *PythonHandler) PassesFilter(proc *Process, filter ProcessFilter) bool {
	if filter.CurrentUserOnly {
		return proc.Owner == currentUser()
	}
	return true
}

// ToServiceSetting converts a Python Process into a ServiceSetting for
// backend reporting.
func (h *PythonHandler) ToServiceSetting(proc *Process) *ServiceSetting {
	key := fmt.Sprintf("host-python-%s", sanitize(proc.ServiceName))
	isSystemd, unitname := CheckSystemdStatus(proc.PID)
	serviceType := "system"
	if isSystemd {
		serviceType = "systemd"
	}

	if proc.IsInContainer() {
		serviceType = "docker"
	} else if proc.DetailBool(DetailIsCelery) {
		serviceType = "worker"
	}

	return &ServiceSetting{
		PID:            proc.PID,
		ServiceName:    proc.ServiceName,
		Owner:          proc.Owner,
		Status:         proc.Status,
		Enabled:        true,
		ServiceType:    serviceType,
		Language:       "python",
		RuntimeVersion: proc.RuntimeVersion,

		MainClass: proc.DetailString(DetailModulePath),
		JarFile:   proc.DetailString(DetailEntryPoint),

		HasAgent:          proc.HasAgent,
		IsMiddlewareAgent: proc.IsMiddlewareAgent,
		AgentType:         proc.AgentType,
		AgentPath:         proc.AgentPath,
		Instrumented:      proc.HasAgent,

		Key:            key,
		ProcessManager: proc.DetailString(DetailProcessManager),
		SystemdUnit:    unitname,
		Listeners:      proc.Listeners(),
	}
}

// --- Private helpers ---

func (h *PythonHandler) extractPythonInfo(proc *Process, cmdArgs []string) {
	if strings.Contains(proc.ExecutablePath, "/bin/python") {
		proc.Details[DetailVenvPath] = filepath.Dir(filepath.Dir(proc.ExecutablePath))
	}

	for i, arg := range cmdArgs {
		if i == 0 || strings.HasPrefix(arg, "-") {
			continue
		}

		if strings.HasSuffix(arg, ".py") {
			proc.Details[DetailEntryPoint] = arg
			if abs, err := filepath.Abs(arg); err == nil {
				proc.Details[DetailWorkingDirectory] = filepath.Dir(abs)
			}
			break
		}

		if i > 0 && cmdArgs[i-1] == "-m" {
			proc.Details[DetailModulePath] = arg
			break
		}
	}
}

func (h *PythonHandler) extractServiceName(proc *Process, cmdArgs []string) {
	// Level 1: Explicit Environment
	if name := extractServiceNameFromEnviron(proc.PID); name != "" {
		proc.ServiceName = cleanName(name)
		return
	}

	// Level 2: Container name
	if proc.ContainerInfo != nil && proc.ContainerInfo.IsContainer {
		if proc.ContainerInfo.ContainerName != "" {
			proc.ServiceName = proc.ContainerInfo.ContainerName
			return
		}
	}

	// Level 3: VirtualEnv path analysis
	for _, arg := range proc.CommandArgs {
		if strings.Contains(arg, "/.venv/") || strings.Contains(arg, "/venv/") || strings.Contains(arg, "/.env/") {
			cleanPath := filepath.Clean(arg)
			parts := strings.Split(cleanPath, string(os.PathSeparator))

			for i, part := range parts {
				if part == ".venv" || part == "venv" || part == ".env" {
					if i > 0 && !isGenericPython(parts[i-1]) {
						proc.ServiceName = parts[i-1]
						return
					}
				}
			}
		}
	}

	// Level 4: Entry point / module analysis
	for i, arg := range cmdArgs {
		if strings.Contains(arg, ":") && !strings.Contains(arg, "/") {
			modName := strings.Split(arg, ":")[0]
			if !isGenericPython(modName) {
				proc.ServiceName = modName
				return
			}
		}

		if strings.HasSuffix(arg, ".py") || (i > 0 && !strings.HasPrefix(arg, "-") && strings.Contains(arg, "/")) {
			baseName := filepath.Base(arg)
			baseName = strings.TrimSuffix(baseName, ".py")
			if !isGenericPython(baseName) {
				proc.ServiceName = baseName
				return
			}
		}
	}

	// Level 5: Absolute script path analysis
	entryPoint := proc.DetailString(DetailEntryPoint)
	if entryPoint != "" {
		fullPath := entryPoint
		workDir := proc.DetailString(DetailWorkingDirectory)
		if !filepath.IsAbs(fullPath) && workDir != "" {
			fullPath = filepath.Join(workDir, fullPath)
		}

		parentDir := filepath.Dir(fullPath)
		dirName := filepath.Base(parentDir)

		if !isGenericPython(dirName) && dirName != "." && dirName != "/" {
			proc.ServiceName = dirName
			return
		}
	}

	// Level 6: Working directory fallback
	workDir := proc.DetailString(DetailWorkingDirectory)
	if workDir != "" {
		dirName := filepath.Base(workDir)
		if !isGenericPython(dirName) && dirName != "." && dirName != "/" {
			proc.ServiceName = dirName
			return
		}
	}

	proc.ServiceName = "python-service"
}

func (h *PythonHandler) detectProcessManager(proc *Process, cmdArgs []string) {
	cmdlineLower := strings.ToLower(strings.Join(cmdArgs, " "))

	if strings.Contains(cmdlineLower, "gunicorn") {
		proc.Details[DetailIsGunicorn] = true
		proc.Details[DetailProcessManager] = "gunicorn"
	} else if strings.Contains(cmdlineLower, "uvicorn") {
		proc.Details[DetailIsUvicorn] = true
		proc.Details[DetailProcessManager] = "uvicorn"
	} else if strings.Contains(cmdlineLower, "celery") {
		proc.Details[DetailIsCelery] = true
		proc.Details[DetailProcessManager] = "celery"
	}
}

func (h *PythonHandler) detectInstrumentation(proc *Process, cmdArgs []string) {
	cmdline := strings.Join(cmdArgs, " ")

	if strings.Contains(cmdline, "opentelemetry-instrument") {
		proc.HasAgent = true
		proc.AgentType = PythonAgentOpenTelemetry.String()
	}

	environPath := fmt.Sprintf("/proc/%d/environ", proc.PID)
	data, err := os.ReadFile(environPath)
	if err != nil {
		return
	}
	env := string(data)

	if strings.Contains(env, "PYTHONPATH") && strings.Contains(env, "mw_bootstrap") {
		proc.HasAgent = true
		proc.IsMiddlewareAgent = true
		proc.AgentType = PythonAgentMiddleware.String()
	}

	if strings.Contains(env, "PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX=") &&
		strings.Contains(env, "LD_PRELOAD="+defaultLibOtelInjectorPath) {
		proc.HasAgent = true
		proc.AgentType = PythonAgentOtelInjector.String()
		proc.AgentPath = defaultPythonAgentBasePath
	}
}

// --- Python-specific lookup tables, types, and helpers ---

// pythonExecutables lists executable names that identify a Python process.
var pythonExecutables = map[string]bool{
	"python":  true,
	"python2": true,
	"python3": true,
	"pypy":    true,
	"pypy3":   true,
}

// pythonBinaries lists Python-based binary names (e.g. web servers, task queues)
// that should be treated as Python processes.
var pythonBinaries = map[string]bool{
	"gunicorn":     true,
	"uvicorn":      true,
	"celery":       true,
	"flask":        true,
	"django-admin": true,
}

// pythonCmdPatterns lists command line patterns that indicate a Python process.
var pythonCmdPatterns = []string{
	"python ",
	"python3 ",
	"gunicorn ",
	"uvicorn ",
	"celery ",
	"manage.py runserver",
	"flask run",
}

// PythonAgentType represents the type of Python instrumentation agent detected.
type PythonAgentType int

const (
	PythonAgentNone          PythonAgentType = iota
	PythonAgentOpenTelemetry                 // opentelemetry-instrument wrapper
	PythonAgentMiddleware                    // mw_bootstrap injected into PYTHONPATH
	PythonAgentOtelInjector                  // LD_PRELOAD + libotelinject.so drop-in
	PythonAgentOther
)

// String returns a human-readable label for the Python agent type.
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

const (
	// defaultLibOtelInjectorPath is the default LD_PRELOAD path for the
	// OpenTelemetry injector shared library.
	defaultLibOtelInjectorPath = "/usr/lib/opentelemetry/libotelinject.so"
	// defaultPythonAgentBasePath is the default base path for the Python
	// OpenTelemetry agent installation.
	defaultPythonAgentBasePath = "/opt/otel-python-agent"
)

// isGenericPython returns true if the name is too generic to be useful as
// a Python service name (e.g. "app", "main", "server", "python").
func isGenericPython(name string) bool {
	generics := map[string]bool{
		"app": true, "main": true, "server": true, "index": true,
		"python": true, "python3": true, "uvicorn": true, "gunicorn": true,
		"bin": true, "src": true, "lib": true,
	}
	return generics[strings.ToLower(name)]
}
