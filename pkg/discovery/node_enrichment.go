package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// enrichNodeProcess takes raw ProcessInfo and produces a fully populated
// NodeProcess. Extracted from the old process.go processOneNode.
func enrichNodeProcess(info *ProcessInfo, opts DiscoveryOptions, detector *ContainerDetector) *NodeProcess {
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

		return &NodeProcess{
			ProcessPID:            pid,
			ProcessParentPID:      readProcessPPID(pid),
			ProcessExecutableName: info.ExeName,
			ProcessExecutablePath: info.ExePath,
			ProcessCommand:        info.CmdLine,
			ProcessCommandLine:    info.CmdLine,
			ProcessOwner:          cached.Owner,
			ProcessCreateTime:     time.Unix(createTime/1000, 0),
			Status:                status,

			ServiceName:           cached.ServiceName,
			ProcessRuntimeVersion: cached.RuntimeVersion,
			EntryPoint:            cached.EntryPoint,
			HasNodeAgent:          cached.HasAgent,
			IsMiddlewareAgent:     cached.IsMiddlewareAgent,
			NodeAgentPath:         cached.AgentPath,
			ProcessManager:        cached.ServiceType,
			IsPM2Process:          cached.ServiceType == "pm2",
			IsForeverProcess:      cached.ServiceType == "forever",
			ContainerInfo:         cached.ContainerInfo,

			ProcessRuntimeName:        "node",
			ProcessRuntimeDescription: "Node.js Runtime",
		}
	}

	// Slow path
	if isIgnoredSystemdUnit(pid) {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true})
		return nil
	}

	status := readProcessStatus(pid)

	nodeProc := &NodeProcess{
		ProcessPID:            pid,
		ProcessParentPID:      readProcessPPID(pid),
		ProcessExecutableName: info.ExeName,
		ProcessExecutablePath: info.ExePath,
		ProcessCommand:        info.CmdLine,
		ProcessCommandLine:    info.CmdLine,
		ProcessCommandArgs:    cmdArgs,
		ProcessOwner:          owner,
		ProcessCreateTime:     time.Unix(createTime/1000, 0),
		Status:                status,

		ProcessRuntimeName:        "node",
		ProcessRuntimeVersion:     "unknown",
		ProcessRuntimeDescription: "Node.js Runtime",
	}

	// Container detection
	if opts.IncludeContainerInfo || opts.ExcludeContainers {
		containerInfo, err := detector.IsProcessInContainer(pid)
		if err == nil {
			nodeProc.ContainerInfo = containerInfo

			if opts.ExcludeContainers && containerInfo.IsContainer {
				return nil
			}

			if containerInfo.IsContainer && containerInfo.ContainerID != "" {
				name := detector.GetContainerNameByID(containerInfo.ContainerID, containerInfo.Runtime)
				if name != "" {
					nodeProc.ContainerInfo.ContainerName = strings.TrimPrefix(name, "/")
				}
			}
		}
	}

	extractNodeInfoFromArgs(nodeProc, cmdArgs)
	extractNodeServiceNameFromArgs(nodeProc, cmdArgs)
	detectNodeProcessManager(nodeProc, cmdArgs)
	detectNodeInstrumentationFromArgs(nodeProc, cmdArgs)

	// Populate cache
	CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{
		ServiceName:       nodeProc.ServiceName,
		ServiceType:       nodeProc.ProcessManager,
		RuntimeVersion:    nodeProc.ProcessRuntimeVersion,
		EntryPoint:        nodeProc.EntryPoint,
		HasAgent:          nodeProc.HasNodeAgent,
		IsMiddlewareAgent: nodeProc.IsMiddlewareAgent,
		AgentPath:         nodeProc.NodeAgentPath,
		ContainerInfo:     nodeProc.ContainerInfo,
		Owner:             nodeProc.ProcessOwner,
	})

	return nodeProc
}

// --- Node info extraction ---

func extractNodeInfoFromArgs(nodeProc *NodeProcess, cmdArgs []string) {
	var entryPoint, workingDirectory string

	if wd, err := os.Getwd(); err == nil {
		workingDirectory = wd
	}

	for i, arg := range cmdArgs {
		if i == 0 || strings.HasPrefix(arg, "-") {
			continue
		}

		if strings.HasSuffix(arg, ".js") || strings.HasSuffix(arg, ".mjs") || strings.HasSuffix(arg, ".ts") {
			entryPoint = arg

			if !filepath.IsAbs(entryPoint) {
				if absPath, err := filepath.Abs(entryPoint); err == nil {
					workingDirectory = filepath.Dir(absPath)
					entryPoint = filepath.Base(absPath)
				}
			} else {
				workingDirectory = filepath.Dir(entryPoint)
				entryPoint = filepath.Base(entryPoint)
			}
			break
		}
	}

	nodeProc.EntryPoint = entryPoint
	nodeProc.WorkingDirectory = workingDirectory

	if workingDirectory != "" {
		extractNodePackageInfo(nodeProc, workingDirectory)
	}
}

func extractNodePackageInfo(nodeProc *NodeProcess, workingDir string) {
	packageJsonPath := filepath.Join(workingDir, "package.json")
	nodeProc.PackageJsonPath = packageJsonPath

	if _, err := os.Stat(packageJsonPath); err == nil {
		nodeProc.PackageName = "unknown"
		nodeProc.PackageVersion = "unknown"
	}
}

// --- Service name extraction ---

func extractNodeServiceNameFromArgs(nodeProc *NodeProcess, cmdArgs []string) {
	if unitName := extractSystemdUnit(nodeProc.ProcessPID); unitName != "" {
		nodeProc.ServiceName = cleanName(unitName)
		return
	}

	if name := extractNodeServiceNameFromCmdArgs(cmdArgs); name != "" {
		nodeProc.ServiceName = name
		return
	}

	if nodeProc.PackageName != "" && nodeProc.PackageName != "unknown" {
		if name := cleanName(nodeProc.PackageName); name != "" {
			nodeProc.ServiceName = name
			return
		}
	}

	if nodeProc.EntryPoint != "" {
		if name := extractNameFromNodeScript(nodeProc.EntryPoint); name != "" {
			nodeProc.ServiceName = name
			return
		}
	}

	if nodeProc.WorkingDirectory != "" {
		if name := extractNameFromDir(nodeProc.WorkingDirectory); name != "" {
			nodeProc.ServiceName = name
			return
		}
	}

	nodeProc.ServiceName = "node-service"
}

func extractNodeServiceNameFromCmdArgs(cmdArgs []string) string {
	serviceProperties := []string{"--name=", "--service=", "SERVICE_NAME=", "NODE_ENV="}

	for _, arg := range cmdArgs {
		for _, prop := range serviceProperties {
			if strings.Contains(arg, prop) {
				name := strings.TrimPrefix(arg, prop)
				name = strings.Trim(name, `"'`)
				if name != "" && name != "production" && name != "development" {
					return cleanName(name)
				}
			}
		}
	}
	return ""
}

func extractNameFromNodeScript(scriptName string) string {
	if scriptName == "" {
		return ""
	}

	baseName := strings.TrimSuffix(scriptName, filepath.Ext(scriptName))
	baseName = filepath.Base(baseName)

	genericNodeNames := map[string]bool{
		"index": true, "app": true, "main": true, "server": true, "start": true, "run": true,
		"node": true, "npm": true, "yarn": true, "nodemon": true, "pm2": true, "forever": true,
	}

	if genericNodeNames[strings.ToLower(baseName)] {
		return ""
	}

	return cleanName(baseName)
}

// --- Process manager detection ---

func detectNodeProcessManager(nodeProc *NodeProcess, cmdArgs []string) {
	cmdline := strings.ToLower(strings.Join(cmdArgs, " "))

	if strings.Contains(cmdline, "pm2") || strings.Contains(cmdline, "/pm2/") {
		nodeProc.IsPM2Process = true
		nodeProc.ProcessManager = "pm2"

		for _, arg := range cmdArgs {
			if strings.HasPrefix(arg, "--name=") {
				nodeProc.PM2ProcessName = strings.TrimPrefix(arg, "--name=")
				break
			}
		}
	}

	if strings.Contains(cmdline, "forever") || strings.Contains(cmdline, "/forever/") {
		nodeProc.IsForeverProcess = true
		nodeProc.ProcessManager = "forever"
	}

	if nodeProc.ProcessManager == "" && nodeProc.ProcessOwner != "root" && nodeProc.ProcessOwner != "node" {
		nodeProc.ProcessManager = "systemd"
	}
}

// --- Instrumentation detection ---

func detectNodeInstrumentationFromArgs(nodeProc *NodeProcess, cmdArgs []string) {
	nodeProc.HasNodeAgent = false
	nodeProc.IsMiddlewareAgent = false
	nodeProc.NodeAgentPath = ""

	cmdline := strings.Join(cmdArgs, " ")

	if detectNodeAgentInCmdline(nodeProc, cmdline) {
		return
	}

	checkNodeEnvForAgent(nodeProc)
}

func detectNodeAgentInCmdline(nodeProc *NodeProcess, cmdline string) bool {
	agentPatterns := []string{"--require ", "-r ", "--import ", "--loader "}

	cmdlineLower := strings.ToLower(cmdline)
	for _, pattern := range agentPatterns {
		if strings.Contains(cmdlineLower, pattern) {
			agentPath := extractNodeAgentPathFromCmdline(cmdline, pattern)
			if agentPath != "" {
				setNodeAgent(nodeProc, agentPath)
				return true
			}
		}
	}
	return false
}

func extractNodeAgentPathFromCmdline(cmdline, pattern string) string {
	idx := strings.Index(strings.ToLower(cmdline), pattern)
	if idx == -1 {
		return ""
	}

	agentPart := cmdline[idx+len(pattern):]
	if spaceIdx := strings.Index(agentPart, " "); spaceIdx != -1 {
		agentPart = agentPart[:spaceIdx]
	}
	return strings.TrimSpace(agentPart)
}

func setNodeAgent(nodeProc *NodeProcess, agentPath string) {
	nodeProc.HasNodeAgent = true
	nodeProc.NodeAgentPath = agentPath

	mwPatterns := []string{"middleware", "mw-", "mw.js", "middleware-agent", "mw-register"}
	lower := strings.ToLower(agentPath)
	for _, p := range mwPatterns {
		if strings.Contains(lower, p) {
			nodeProc.IsMiddlewareAgent = true
			return
		}
	}
}

func checkNodeEnvForAgent(nodeProc *NodeProcess) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", nodeProc.ProcessPID))
	if err != nil {
		return
	}

	envVars := strings.Split(string(data), "\x00")
	for _, env := range envVars {
		if strings.HasPrefix(env, "NODE_OPTIONS=") {
			value := strings.TrimPrefix(env, "NODE_OPTIONS=")
			if agentPath := extractNodeAgentFromEnvValue(value); agentPath != "" {
				setNodeAgent(nodeProc, agentPath)
				return
			}
		}

		if strings.HasPrefix(env, "LD_PRELOAD=") {
			value := strings.TrimPrefix(env, "LD_PRELOAD=")
			if path := extractLibOtelInjectPath(value); path != "" {
				nodeProc.HasNodeAgent = true
				nodeProc.IsMiddlewareAgent = false
				nodeProc.NodeAgentPath = path
				return
			}
		}
	}
}

func extractNodeAgentFromEnvValue(envValue string) string {
	agentPatterns := []string{"--require ", "-r ", "--import ", "--loader "}
	for _, pattern := range agentPatterns {
		if idx := strings.Index(envValue, pattern); idx != -1 {
			agentPart := envValue[idx+len(pattern):]
			if spaceIdx := strings.Index(agentPart, " "); spaceIdx != -1 {
				agentPart = agentPart[:spaceIdx]
			}
			return agentPart
		}
	}
	return ""
}
