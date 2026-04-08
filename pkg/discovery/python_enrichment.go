package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultLibOtelInjectorPath = "/usr/lib/opentelemetry/libotelinject.so"
	defaultPythonAgentBasePath = "/opt/otel-python-agent"
)

// enrichPythonProcess takes raw ProcessInfo and produces a fully populated
// PythonProcess. Extracted from the old process.go processOnePython.
func enrichPythonProcess(info *ProcessInfo, opts DiscoveryOptions, detector *ContainerDetector) *PythonProcess {
	pid := info.PID
	cmdArgs := info.CmdArgs

	owner := readProcessOwner(pid)
	createTime := readProcessCreateTime(pid)
	alignedTime := (createTime / 1000) * 1000

	// Cache check
	if cached, hit := GetCachedProcessMetadata(pid, alignedTime); hit {
		if cached.Ignore {
			return nil
		}

		status := readProcessStatus(pid)

		return &PythonProcess{
			ProcessPID:            pid,
			ProcessParentPID:      readProcessPPID(pid),
			ProcessExecutablePath: info.ExePath,
			ProcessExecutableName: info.ExeName,
			ProcessCommandLine:    info.CmdLine,
			ProcessOwner:          cached.Owner,
			ProcessCreateTime:     time.Unix(createTime/1000, 0),
			Status:                status,

			ServiceName:           cached.ServiceName,
			ProcessManager:        cached.ServiceType,
			ProcessRuntimeVersion: cached.RuntimeVersion,
			EntryPoint:            cached.EntryPoint,
			HasPythonAgent:        cached.HasAgent,
			IsMiddlewareAgent:     cached.IsMiddlewareAgent,
			PythonAgentPath:       cached.AgentPath,
			PythonAgentType:       cached.PythonAgentType,
			ContainerInfo:         cached.ContainerInfo,

			ProcessRuntimeName:        "python",
			ProcessRuntimeDescription: "Python Interpreter",
		}
	}

	// Slow path
	if isIgnoredSystemdUnit(pid) {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true})
		return nil
	}

	status := readProcessStatus(pid)

	pyProc := &PythonProcess{
		ProcessPID:            pid,
		ProcessParentPID:      readProcessPPID(pid),
		ProcessExecutablePath: info.ExePath,
		ProcessExecutableName: info.ExeName,
		ProcessCommandLine:    info.CmdLine,
		ProcessCommandArgs:    cmdArgs,
		ProcessOwner:          owner,
		ProcessCreateTime:     time.Unix(createTime/1000, 0),
		Status:                status,

		ProcessRuntimeName:        "python",
		ProcessRuntimeDescription: "Python Interpreter",
	}

	// Container detection
	if opts.IncludeContainerInfo {
		containerInfo, err := detector.IsProcessInContainer(pid)
		if err == nil && containerInfo.IsContainer {
			pyProc.ContainerInfo = containerInfo

			if containerInfo.ContainerName == "" && containerInfo.ContainerID != "" {
				name := detector.GetContainerNameByID(containerInfo.ContainerID, containerInfo.Runtime)
				if name != "" {
					pyProc.ContainerInfo.ContainerName = strings.TrimPrefix(name, "/")
				}
			}
		}
	}

	// Skip multiprocessing sub-processes
	isSubProcess := strings.Contains(info.CmdLine, "multiprocessing.spawn") ||
		strings.Contains(info.CmdLine, "resource_tracker")
	if isSubProcess {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true, Owner: owner})
		return nil
	}

	extractPythonInfoFromArgs(pyProc, cmdArgs)
	extractPythonServiceNameFromArgs(pyProc, cmdArgs)
	detectPythonManager(pyProc, cmdArgs)
	detectPythonInstrumentationFromArgs(pyProc, cmdArgs)

	// Populate cache
	CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{
		ServiceName:       pyProc.ServiceName,
		ServiceType:       pyProc.ProcessManager,
		RuntimeVersion:    pyProc.ProcessRuntimeVersion,
		EntryPoint:        pyProc.EntryPoint,
		HasAgent:          pyProc.HasPythonAgent,
		IsMiddlewareAgent: pyProc.IsMiddlewareAgent,
		AgentPath:         pyProc.PythonAgentPath,
		PythonAgentType:   pyProc.PythonAgentType,
		ContainerInfo:     pyProc.ContainerInfo,
		Owner:             pyProc.ProcessOwner,
	})

	return pyProc
}

// --- Python info extraction ---

func extractPythonInfoFromArgs(pyProc *PythonProcess, cmdArgs []string) {
	if strings.Contains(pyProc.ProcessExecutablePath, "/bin/python") {
		pyProc.VirtualEnvPath = filepath.Dir(filepath.Dir(pyProc.ProcessExecutablePath))
	}

	for i, arg := range cmdArgs {
		if i == 0 || strings.HasPrefix(arg, "-") {
			continue
		}

		if strings.HasSuffix(arg, ".py") {
			pyProc.EntryPoint = arg
			if abs, err := filepath.Abs(arg); err == nil {
				pyProc.WorkingDirectory = filepath.Dir(abs)
			}
			break
		}

		if i > 0 && cmdArgs[i-1] == "-m" {
			pyProc.ModulePath = arg
			break
		}
	}
}

// --- Service name extraction ---

func extractPythonServiceNameFromArgs(pyProc *PythonProcess, cmdArgs []string) {
	// Level 1: Explicit Environment
	if name := extractServiceNameFromEnviron(pyProc.ProcessPID); name != "" {
		pyProc.ServiceName = cleanName(name)
		return
	}

	// Level 2: Container name
	if pyProc.ContainerInfo != nil && pyProc.ContainerInfo.IsContainer {
		if pyProc.ContainerInfo.ContainerName != "" {
			pyProc.ServiceName = pyProc.ContainerInfo.ContainerName
			return
		}
	}

	// Level 3: VirtualEnv path analysis
	for _, arg := range pyProc.ProcessCommandArgs {
		if strings.Contains(arg, "/.venv/") || strings.Contains(arg, "/venv/") || strings.Contains(arg, "/.env/") {
			cleanPath := filepath.Clean(arg)
			parts := strings.Split(cleanPath, string(os.PathSeparator))

			for i, part := range parts {
				if part == ".venv" || part == "venv" || part == ".env" {
					if i > 0 && !isGenericPython(parts[i-1]) {
						pyProc.ServiceName = parts[i-1]
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
				pyProc.ServiceName = modName
				return
			}
		}

		if strings.HasSuffix(arg, ".py") || (i > 0 && !strings.HasPrefix(arg, "-") && strings.Contains(arg, "/")) {
			baseName := filepath.Base(arg)
			baseName = strings.TrimSuffix(baseName, ".py")
			if !isGenericPython(baseName) {
				pyProc.ServiceName = baseName
				return
			}
		}
	}

	// Level 5: Absolute script path analysis
	if pyProc.EntryPoint != "" {
		fullPath := pyProc.EntryPoint
		if !filepath.IsAbs(fullPath) && pyProc.WorkingDirectory != "" {
			fullPath = filepath.Join(pyProc.WorkingDirectory, fullPath)
		}

		parentDir := filepath.Dir(fullPath)
		dirName := filepath.Base(parentDir)

		if !isGenericPython(dirName) && dirName != "." && dirName != "/" {
			pyProc.ServiceName = dirName
			return
		}
	}

	// Level 6: Working directory fallback
	if pyProc.WorkingDirectory != "" {
		dirName := filepath.Base(pyProc.WorkingDirectory)
		if !isGenericPython(dirName) && dirName != "." && dirName != "/" {
			pyProc.ServiceName = dirName
			return
		}
	}

	pyProc.ServiceName = "python-service"
}

func isGenericPython(name string) bool {
	generics := map[string]bool{
		"app": true, "main": true, "server": true, "index": true,
		"python": true, "python3": true, "uvicorn": true, "gunicorn": true,
		"bin": true, "src": true, "lib": true,
	}
	return generics[strings.ToLower(name)]
}

// --- Process manager detection ---

func detectPythonManager(pyProc *PythonProcess, cmdArgs []string) {
	cmdlineLower := strings.ToLower(strings.Join(cmdArgs, " "))

	if strings.Contains(cmdlineLower, "gunicorn") {
		pyProc.IsGunicornProcess = true
		pyProc.ProcessManager = "gunicorn"
	} else if strings.Contains(cmdlineLower, "uvicorn") {
		pyProc.IsUvicornProcess = true
		pyProc.ProcessManager = "uvicorn"
	} else if strings.Contains(cmdlineLower, "celery") {
		pyProc.IsCeleryProcess = true
		pyProc.ProcessManager = "celery"
	}
}

// --- Instrumentation detection ---

func detectPythonInstrumentationFromArgs(pyProc *PythonProcess, cmdArgs []string) {
	cmdline := strings.Join(cmdArgs, " ")

	if strings.Contains(cmdline, "opentelemetry-instrument") {
		pyProc.HasPythonAgent = true
		pyProc.PythonAgentType = PythonAgentOpenTelemetry
	}

	environPath := fmt.Sprintf("/proc/%d/environ", pyProc.ProcessPID)
	data, err := os.ReadFile(environPath)
	if err != nil {
		return
	}
	env := string(data)

	if strings.Contains(env, "PYTHONPATH") && strings.Contains(env, "mw_bootstrap") {
		pyProc.HasPythonAgent = true
		pyProc.IsMiddlewareAgent = true
		pyProc.PythonAgentType = PythonAgentMiddleware
	}

	if strings.Contains(env, "PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX=") &&
		strings.Contains(env, "LD_PRELOAD="+defaultLibOtelInjectorPath) {
		pyProc.HasPythonAgent = true
		pyProc.PythonAgentType = PythonAgentOtelInjector
		pyProc.PythonAgentPath = defaultPythonAgentBasePath
	}
}
