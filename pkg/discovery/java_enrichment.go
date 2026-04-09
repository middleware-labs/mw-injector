package discovery

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// enrichJavaProcess takes raw ProcessInfo and produces a fully populated
// JavaProcess by extracting JVM options, JAR info, Tomcat deployment,
// instrumentation, service name, and container info.
//
// This is the "slow path" logic extracted from the old process.go processOne.
func enrichJavaProcess(info *ProcessInfo, opts DiscoveryOptions, detector *ContainerDetector) *JavaProcess {
	pid := info.PID
	cmdArgs := info.CmdArgs

	owner := readProcessOwner(pid)
	createTime := readProcessCreateTime(pid)
	alignedTime := (createTime / 1000) * 1000

	if cached, hit := GetCachedProcessMetadata(pid, alignedTime); hit && cached.Ignore {
		return nil
	}

	if isIgnoredSystemdUnit(pid) {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true})
		return nil
	}

	status := readProcessStatus(pid)

	javaProc := &JavaProcess{
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

		ProcessRuntimeName:        "java",
		ProcessRuntimeVersion:     extractJavaVersionFromArgs(cmdArgs),
		ProcessRuntimeDescription: "Java Virtual Machine",
	}

	// Container detection
	if opts.IncludeContainerInfo || opts.ExcludeContainers {
		containerInfo, err := detector.IsProcessInContainer(pid)
		if err == nil {
			javaProc.ContainerInfo = containerInfo

			if opts.ExcludeContainers && containerInfo.IsContainer {
				return nil
			}

			if containerInfo.IsContainer && containerInfo.ContainerID != "" {
				name := detector.GetContainerNameByID(containerInfo.ContainerID, containerInfo.Runtime)
				if name != "" {
					javaProc.ContainerInfo.ContainerName = strings.TrimPrefix(name, "/")
				}
			}
		}
	}

	extractJavaInfoFromArgs(javaProc, cmdArgs)
	extractJavaServiceName(javaProc, cmdArgs)

	tomcatInfo := detectTomcatFromArgs(javaProc, cmdArgs)
	if tomcatInfo.IsTomcat {
		if javaProc.ServiceName == "" || javaProc.ServiceName == "java-service" {
			if tomcatInfo.InstanceName != "" {
				javaProc.ServiceName = tomcatInfo.InstanceName
			} else {
				javaProc.ServiceName = "tomcat"
			}
		}
	}

	detectJavaInstrumentation(javaProc, cmdArgs)

	return javaProc
}

// --- JVM Info extraction (from old service.go extractJavaInfo) ---

func extractJavaInfoFromArgs(javaProc *JavaProcess, cmdArgs []string) {
	var jvmOptions []string
	var jarFile, jarPath, mainClass string

	for i, arg := range cmdArgs {
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "-jar") {
			if arg != "-jar" {
				jvmOptions = append(jvmOptions, arg)
			}
		}

		if arg == "-jar" && i+1 < len(cmdArgs) {
			jarPath = cmdArgs[i+1]
			jarFile = filepath.Base(jarPath)
		}

		if !strings.HasPrefix(arg, "-") && !strings.HasSuffix(arg, ".jar") &&
			!strings.Contains(arg, "/") && strings.Contains(arg, ".") {
			mainClass = arg
		}
	}

	javaProc.JVMOptions = jvmOptions
	javaProc.JarFile = jarFile
	javaProc.JarPath = jarPath
	javaProc.MainClass = mainClass
}

func extractJavaVersionFromArgs(args []string) string {
	for i, arg := range args {
		if arg == "-version" && i > 0 {
			return "unknown"
		}
	}
	return "unknown"
}

// --- Service name extraction (from old service.go) ---

func extractJavaServiceName(javaProc *JavaProcess, cmdArgs []string) {
	if javaProc.IsInContainer() && javaProc.ContainerInfo.ContainerName != "" {
		javaProc.ServiceName = javaProc.ContainerInfo.ContainerName
		return
	}

	if envName := extractServiceNameFromEnviron(javaProc.ProcessPID); envName != "" {
		javaProc.ServiceName = cleanName(envName)
		return
	}

	if unitName := extractSystemdUnit(javaProc.ProcessPID); unitName != "" {
		javaProc.ServiceName = cleanName(unitName)
		return
	}

	if propName := extractFromJavaSystemProperties(cmdArgs); propName != "" {
		javaProc.ServiceName = propName
		return
	}

	if javaProc.JarFile != "" {
		name := extractNameFromJar(javaProc.JarFile)
		if !isGenericJava(name) {
			javaProc.ServiceName = name
			return
		}
	}

	if javaProc.MainClass != "" {
		name := extractNameFromMainClass(javaProc.MainClass)
		if !isGenericJava(name) {
			javaProc.ServiceName = name
			return
		}
	}

	if javaProc.JarPath != "" {
		name := extractNameFromDir(javaProc.JarPath)
		if !isGenericJava(name) {
			javaProc.ServiceName = name
			return
		}
	}

	javaProc.ServiceName = "java-service"
}

// --- Instrumentation detection (from old instrumentation.go) ---

func detectJavaInstrumentation(javaProc *JavaProcess, cmdArgs []string) {
	javaProc.HasJavaAgent = false
	javaProc.IsMiddlewareAgent = false
	javaProc.JavaAgentPath = ""
	javaProc.JavaAgentName = ""

	for _, arg := range cmdArgs {
		if strings.HasPrefix(arg, "-javaagent:") {
			agentPath := strings.TrimPrefix(arg, "-javaagent:")
			setJavaAgentInfo(javaProc, agentPath)
			return
		}
	}

	checkJavaEnvForAgent(javaProc)
}

func checkJavaEnvForAgent(javaProc *JavaProcess) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", javaProc.ProcessPID))
	if err != nil {
		return
	}

	envVars := strings.Split(string(data), "\x00")
	for _, env := range envVars {
		for _, prefix := range []string{"JAVA_TOOL_OPTIONS=", "CATALINA_OPTS=", "JAVA_OPTS="} {
			if strings.HasPrefix(env, prefix) {
				value := strings.TrimPrefix(env, prefix)
				if agentPath := extractJavaAgentFromEnvValue(value); agentPath != "" {
					setJavaAgentInfo(javaProc, agentPath)
					return
				}
			}
		}

		if strings.HasPrefix(env, "LD_PRELOAD=") {
			value := strings.TrimPrefix(env, "LD_PRELOAD=")
			if path := extractLibOtelInjectPath(value); path != "" {
				javaProc.HasJavaAgent = true
				javaProc.IsMiddlewareAgent = false
				javaProc.JavaAgentPath = path
				return
			}
		}
	}
}

func extractJavaAgentFromEnvValue(envValue string) string {
	if idx := strings.Index(envValue, "-javaagent:"); idx != -1 {
		agentPart := envValue[idx+len("-javaagent:"):]
		if spaceIdx := strings.Index(agentPart, " "); spaceIdx != -1 {
			agentPart = agentPart[:spaceIdx]
		}
		return extractAgentJarPath(agentPart)
	}
	return ""
}

func setJavaAgentInfo(javaProc *JavaProcess, agentPath string) {
	agentPath = extractAgentJarPath(agentPath)
	javaProc.HasJavaAgent = true
	javaProc.JavaAgentPath = agentPath
	javaProc.JavaAgentName = filepath.Base(agentPath)

	if detectJavaAgentType(agentPath) == AgentMiddleware {
		javaProc.IsMiddlewareAgent = true
	}
}

func extractAgentJarPath(agentArg string) string {
	if idx := strings.Index(agentArg, "="); idx != -1 {
		return agentArg[:idx]
	}
	return agentArg
}

func detectJavaAgentType(agentPath string) AgentType {
	lower := strings.ToLower(agentPath)
	name := strings.ToLower(filepath.Base(agentPath))

	mwPatterns := []string{"middleware", "mw-", "mw.jar", "middleware-javaagent", "mw-javaagent"}
	for _, p := range mwPatterns {
		if strings.Contains(lower, p) || strings.Contains(name, p) {
			return AgentMiddleware
		}
	}

	otelPatterns := []string{"opentelemetry", "otel"}
	for _, p := range otelPatterns {
		if strings.Contains(lower, p) || strings.Contains(name, p) {
			return AgentOpenTelemetry
		}
	}

	return AgentOther
}

// --- Tomcat detection (delegate to existing tomcat.go via discoverer-free path) ---

func detectTomcatFromArgs(javaProc *JavaProcess, cmdArgs []string) *TomcatInfo {
	d := &discoverer{}
	return d.detectTomcatDeployment(javaProc, cmdArgs)
}

// --- Shared helpers ---

func extractServiceNameFromEnviron(pid int32) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return ""
	}

	envVars := strings.Split(string(data), "\x00")
	for _, env := range envVars {
		if strings.HasPrefix(env, "OTEL_SERVICE_NAME=") ||
			strings.HasPrefix(env, "SERVICE_NAME=") ||
			strings.HasPrefix(env, "FLASK_APP=") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) > 1 && parts[1] != "" {
				return parts[1]
			}
		}
	}
	return ""
}

func extractSystemdUnit(pid int32) string {
	name, found := parseCgroupUnitName(pid)
	if !found || ignoredSystemdUnits[name] {
		return ""
	}
	return name
}

func isIgnoredSystemdUnit(pid int32) bool {
	name, found := parseCgroupUnitName(pid)
	return found && ignoredSystemdUnits[name]
}

func extractFromJavaSystemProperties(cmdArgs []string) string {
	serviceProperties := []string{
		"-Dotel.service.name=",
		"-Dservice.name=",
		"-Dspring.application.name=",
		"-Dapplication.name=",
		"-Dmw.service.name=",
		"-DOTEL_SERVICE_NAME=",
		"-DSERVICE_NAME=",
	}

	for _, arg := range cmdArgs {
		for _, prop := range serviceProperties {
			if strings.HasPrefix(arg, prop) {
				name := strings.TrimPrefix(arg, prop)
				name = strings.Trim(name, `"'`)
				if name != "" {
					return cleanName(name)
				}
			}
		}
	}
	return ""
}

func extractNameFromJar(jarFile string) string {
	if jarFile == "" {
		return ""
	}

	baseName := filepath.Base(jarFile)
	nameNoExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))

	patterns := []struct {
		re  *regexp.Regexp
		rep string
	}{
		{regexp.MustCompile(`-\d+\.\d+\.\d+.*$`), ""},
		{regexp.MustCompile(`-\d+\.\d+.*$`), ""},
		{regexp.MustCompile(`_\d+\.\d+\.\d+.*$`), ""},
		{regexp.MustCompile(`_\d+\.\d+.*$`), ""},
		{regexp.MustCompile(`-SNAPSHOT$`), ""},
		{regexp.MustCompile(`_SNAPSHOT$`), ""},
		{regexp.MustCompile(`-BUILD-\d+$`), ""},
		{regexp.MustCompile(`_BUILD_\d+$`), ""},
	}

	result := nameNoExt
	for _, p := range patterns {
		result = p.re.ReplaceAllString(result, p.rep)
	}

	return cleanName(result)
}

func extractNameFromMainClass(mainClass string) string {
	if mainClass == "" {
		return ""
	}

	parts := strings.Split(mainClass, ".")
	if len(parts) == 0 {
		return ""
	}

	className := parts[len(parts)-1]

	suffixes := []string{"Application", "App", "Service", "Server", "Main", "Launcher", "Bootstrap"}
	for _, s := range suffixes {
		className = strings.TrimSuffix(className, s)
	}

	// CamelCase to kebab-case
	re := regexp.MustCompile(`([a-z])([A-Z])`)
	className = re.ReplaceAllString(className, "${1}-${2}")

	return cleanName(className)
}

func extractNameFromDir(path string) string {
	if path == "" {
		return ""
	}

	dir := filepath.Dir(path)
	pathParts := strings.Split(dir, "/")

	genericDirs := map[string]bool{
		"": true, ".": true, "..": true, "/": true, "home": true, "opt": true,
		"usr": true, "var": true, "tmp": true, "app": true, "apps": true,
		"bin": true, "lib": true, "lib64": true, "java": true, "jvm": true,
		"target": true, "build": true, "classes": true, "web-inf": true,
		"meta-inf": true, "src": true, "main": true, "resources": true,
		"static": true, "public": true,
	}

	var meaningful []string
	for _, part := range pathParts {
		part = strings.TrimSpace(part)
		if part != "" && !genericDirs[strings.ToLower(part)] {
			meaningful = append(meaningful, part)
		}
	}

	if len(meaningful) > 0 {
		return cleanName(meaningful[len(meaningful)-1])
	}
	return ""
}

func isGenericJava(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	generics := map[string]bool{
		"bin": true, "lib": true, "src": true, "target": true,
		"java": true, "jre": true, "jdk": true, "app": true,
		"main": true, "server": true, "tomcat": true,
	}
	return generics[name] || name == "." || name == ""
}

var (
	cleanNameInvalid  = regexp.MustCompile(`[^a-z0-9\-]+`)
	cleanNameMultiDash = regexp.MustCompile(`-+`)
)

func cleanName(name string) string {
	if name == "" {
		return ""
	}
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "_", "-")
	name = cleanNameInvalid.ReplaceAllString(name, "")
	name = strings.Trim(name, "-")
	name = cleanNameMultiDash.ReplaceAllString(name, "-")

	genericServiceNames := map[string]bool{
		"java": true, "app": true, "application": true, "service": true,
		"server": true, "main": true, "demo": true, "test": true,
		"example": true, "sample": true, "hello": true, "world": true,
	}
	if name == "" || genericServiceNames[name] {
		return ""
	}
	return name
}

// --- /proc helpers for process metadata ---

func readProcessOwner(pid int32) string {
	path := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				u, err := user.LookupId(fields[1])
				if err != nil {
					return fields[1]
				}
				return u.Username
			}
		}
	}
	return "unknown"
}

func readProcessPPID(pid int32) int32 {
	path := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				var ppid int32
				fmt.Sscanf(fields[1], "%d", &ppid)
				return ppid
			}
		}
	}
	return 0
}

func readProcessCreateTime(pid int32) int64 {
	path := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	// The start time field is field 22 (1-indexed) in /proc/<pid>/stat.
	// The format is: pid (comm) state ppid ... starttime ...
	// We need to find the closing ')' of comm first, then count fields.
	content := string(data)
	closeParen := strings.LastIndex(content, ")")
	if closeParen == -1 {
		return 0
	}

	fields := strings.Fields(content[closeParen+2:])
	if len(fields) < 20 {
		return 0
	}
	// starttime is field 20 after the closing paren (0-indexed)
	var startTime int64
	fmt.Sscanf(fields[19], "%d", &startTime)

	// Convert jiffies to milliseconds using the actual system clock tick.
	// This isn't a wallclock time; it's used only as a stable fingerprint.
	return startTime * (1000 / clockTickHz)
}

func readProcessStatus(pid int32) string {
	path := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "State:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return "unknown"
}
