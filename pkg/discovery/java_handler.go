// java_handler.go implements the LanguageHandler interface for Java/JVM processes.
// It handles detection of JVM processes, enrichment with JVM options, JAR info,
// Tomcat deployment details, and instrumentation state. Also contains Java-specific
// helper functions for service name extraction and agent detection.
package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// JavaHandler implements LanguageHandler for Java processes.
// It detects JVM processes, enriches them with JVM options, JAR info,
// Tomcat deployment, and instrumentation details, and converts them
// to ServiceSettings for reporting.
type JavaHandler struct{}

// Lang returns LangJava.
func (h *JavaHandler) Lang() Language { return LangJava }

// Detect returns true if the process is a Java/JVM process, identified
// by executable name or command line patterns.
func (h *JavaHandler) Detect(proc *ProcessInfo) bool {
	exeLower := strings.ToLower(proc.ExeName)

	if exeLower == "java" || strings.HasSuffix(exeLower, "/java") {
		return true
	}

	if strings.Contains(exeLower, "java") {
		return true
	}

	cmdLower := strings.ToLower(proc.CmdLine)
	if strings.HasPrefix(cmdLower, "java ") || strings.Contains(cmdLower, "/java ") {
		return true
	}

	return false
}

// Enrich populates a Process struct with Java-specific details by reading
// JVM options, JAR info, Tomcat deployment, and instrumentation state.
// Returns nil if the process should be skipped (cached as ignored, etc.).
func (h *JavaHandler) Enrich(info *ProcessInfo, opts DiscoveryOptions, detector *ContainerDetector) *Process {
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

	proc := &Process{
		PID:            pid,
		ParentPID:      readProcessPPID(pid),
		ExecutableName: info.ExeName,
		ExecutablePath: info.ExePath,
		Command:        info.CmdLine,
		CommandLine:    info.CmdLine,
		CommandArgs:    cmdArgs,
		Owner:          owner,
		CreateTime:     timeFromMillis(createTime),
		Status:         status,
		Language:       LangJava,

		RuntimeName:        "java",
		RuntimeVersion:     extractJavaVersionFromArgs(cmdArgs),
		RuntimeDescription: "Java Virtual Machine",

		Details: make(map[string]any),
	}

	// Container detection
	if opts.IncludeContainerInfo || opts.ExcludeContainers {
		containerInfo, err := detector.IsProcessInContainer(pid)
		if err == nil {
			proc.ContainerInfo = containerInfo

			if opts.ExcludeContainers && containerInfo.IsContainer {
				return nil
			}
		}
	}

	h.extractJavaInfo(proc, cmdArgs)
	h.extractServiceName(proc, cmdArgs)
	h.detectTomcat(proc, cmdArgs)
	h.detectInstrumentation(proc, cmdArgs)

	return proc
}

// PassesFilter checks user ownership and Java-specific agent filters.
func (h *JavaHandler) PassesFilter(proc *Process, filter ProcessFilter) bool {
	if filter.CurrentUserOnly {
		if proc.Owner != currentUser() {
			return false
		}
	}

	if len(filter.IncludeUsers) > 0 {
		found := false
		for _, u := range filter.IncludeUsers {
			if proc.Owner == u {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(filter.ExcludeUsers) > 0 {
		for _, u := range filter.ExcludeUsers {
			if proc.Owner == u {
				return false
			}
		}
	}

	if filter.HasJavaAgentOnly && !proc.HasAgent {
		return false
	}
	if filter.HasMWAgentOnly && !proc.IsMiddlewareAgent {
		return false
	}

	return true
}

// ToServiceSetting converts a Java Process into a ServiceSetting for
// backend reporting. Returns nil for Tomcat processes (they are excluded).
func (h *JavaHandler) ToServiceSetting(proc *Process) *ServiceSetting {
	if proc.DetailBool(DetailIsTomcat) {
		return nil
	}

	key := fmt.Sprintf("host-java-%s", sanitize(proc.ServiceName))
	isSystemd, unitname := CheckSystemdStatus(proc.PID)

	deploymentType := "standalone"
	if proc.IsInContainer() {
		deploymentType = "docker"
	} else if isSystemd {
		deploymentType = "systemd"
	}

	agentType := deriveAgentType(proc.HasAgent, proc.AgentPath, proc.IsMiddlewareAgent)

	return &ServiceSetting{
		PID:               proc.PID,
		ServiceName:       proc.ServiceName,
		Owner:             proc.Owner,
		Status:            proc.Status,
		Enabled:           true,
		ServiceType:       deploymentType,
		Language:          "java",
		RuntimeVersion:    proc.RuntimeVersion,
		JarFile:           proc.DetailString(DetailJarFile),
		MainClass:         proc.DetailString(DetailMainClass),
		HasAgent:          proc.HasAgent,
		IsMiddlewareAgent: proc.IsMiddlewareAgent,
		AgentType:         agentType,
		AgentPath:         proc.AgentPath,
		Instrumented:      proc.HasAgent,
		Key:               key,
		SystemdUnit:       unitname,
		Listeners:         proc.Listeners(),
	}
}

// --- Private helpers ---

func (h *JavaHandler) extractJavaInfo(proc *Process, cmdArgs []string) {
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
			jarFile = fileBase(jarPath)
		}

		if !strings.HasPrefix(arg, "-") && !strings.HasSuffix(arg, ".jar") &&
			!strings.Contains(arg, "/") && strings.Contains(arg, ".") {
			mainClass = arg
		}
	}

	proc.Details[DetailJVMOptions] = jvmOptions
	proc.Details[DetailJarFile] = jarFile
	proc.Details[DetailJarPath] = jarPath
	proc.Details[DetailMainClass] = mainClass
}

func (h *JavaHandler) extractServiceName(proc *Process, cmdArgs []string) {
	if proc.IsInContainer() && proc.ContainerInfo.ContainerName != "" {
		proc.ServiceName = proc.ContainerInfo.ContainerName
		return
	}

	if envName := extractServiceNameFromEnviron(proc.PID); envName != "" {
		proc.ServiceName = cleanName(envName)
		return
	}

	if unitName := extractSystemdUnit(proc.PID); unitName != "" {
		proc.ServiceName = cleanName(unitName)
		return
	}

	if propName := extractFromJavaSystemProperties(cmdArgs); propName != "" {
		proc.ServiceName = propName
		return
	}

	jarFile := proc.DetailString(DetailJarFile)
	if jarFile != "" {
		name := extractNameFromJar(jarFile)
		if !isGenericJava(name) {
			proc.ServiceName = name
			return
		}
	}

	mainClass := proc.DetailString(DetailMainClass)
	if mainClass != "" {
		name := extractNameFromMainClass(mainClass)
		if !isGenericJava(name) {
			proc.ServiceName = name
			return
		}
	}

	jarPath := proc.DetailString(DetailJarPath)
	if jarPath != "" {
		name := extractNameFromDir(jarPath)
		if !isGenericJava(name) {
			proc.ServiceName = name
			return
		}
	}

	proc.ServiceName = "java-service"
}

func (h *JavaHandler) detectTomcat(proc *Process, cmdArgs []string) {
	isTomcat := false
	for _, arg := range cmdArgs {
		argLower := strings.ToLower(arg)
		if strings.Contains(argLower, "catalina") ||
			strings.Contains(argLower, "org.apache.catalina.startup.bootstrap") ||
			strings.Contains(argLower, "tomcat") {
			isTomcat = true
			break
		}
	}

	proc.Details[DetailIsTomcat] = isTomcat

	if isTomcat {
		if proc.ServiceName == "" || proc.ServiceName == "java-service" {
			instanceName := ""
			for _, arg := range cmdArgs {
				if strings.HasPrefix(arg, "-Dcatalina.base=") {
					base := strings.TrimPrefix(arg, "-Dcatalina.base=")
					parts := strings.Split(base, "/")
					if len(parts) > 0 {
						instanceName = parts[len(parts)-1]
					}
				}
			}
			if instanceName != "" {
				proc.ServiceName = instanceName
			} else {
				proc.ServiceName = "tomcat"
			}
		}
	}
}

func (h *JavaHandler) detectInstrumentation(proc *Process, cmdArgs []string) {
	for _, arg := range cmdArgs {
		if strings.HasPrefix(arg, "-javaagent:") {
			agentPath := strings.TrimPrefix(arg, "-javaagent:")
			h.setAgentInfo(proc, agentPath)
			return
		}
	}

	h.checkEnvForAgent(proc)
}

func (h *JavaHandler) checkEnvForAgent(proc *Process) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", proc.PID))
	if err != nil {
		return
	}

	envVars := strings.Split(string(data), "\x00")
	for _, env := range envVars {
		for _, prefix := range []string{"JAVA_TOOL_OPTIONS=", "CATALINA_OPTS=", "JAVA_OPTS="} {
			if strings.HasPrefix(env, prefix) {
				value := strings.TrimPrefix(env, prefix)
				if agentPath := extractJavaAgentFromEnvValue(value); agentPath != "" {
					h.setAgentInfo(proc, agentPath)
					return
				}
			}
		}

		if strings.HasPrefix(env, "LD_PRELOAD=") {
			value := strings.TrimPrefix(env, "LD_PRELOAD=")
			if path := extractLibOtelInjectPath(value); path != "" {
				proc.HasAgent = true
				proc.IsMiddlewareAgent = false
				proc.AgentPath = path
				proc.AgentType = AgentOpenTelemetry.String()
				return
			}
		}
	}
}

func (h *JavaHandler) setAgentInfo(proc *Process, agentPath string) {
	agentPath = extractAgentJarPath(agentPath)
	proc.HasAgent = true
	proc.AgentPath = agentPath
	proc.AgentName = fileBase(agentPath)

	agentType := detectJavaAgentType(agentPath)
	proc.AgentType = agentType.String()
	if agentType == AgentMiddleware {
		proc.IsMiddlewareAgent = true
	}
}

// --- Java-specific service name and agent helpers ---

// extractFromJavaSystemProperties scans JVM args for well-known system
// properties that specify a service name (e.g. -Dotel.service.name=...).
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

// extractNameFromJar derives a service name from a JAR filename by stripping
// version suffixes, SNAPSHOT markers, and build identifiers.
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

// extractNameFromMainClass derives a service name from a fully qualified
// Java main class by taking the short class name and stripping common
// suffixes (Application, App, Service, Server, etc.).
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

	re := regexp.MustCompile(`([a-z])([A-Z])`)
	className = re.ReplaceAllString(className, "${1}-${2}")

	return cleanName(className)
}

// extractNameFromDir attempts to derive a service name from a file's parent
// directory path, skipping generic directories (home, opt, usr, var, etc.).
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

// isGenericJava returns true if the name is too generic to be useful as a
// Java service name (e.g. "bin", "java", "app", "tomcat").
func isGenericJava(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	generics := map[string]bool{
		"bin": true, "lib": true, "src": true, "target": true,
		"java": true, "jre": true, "jdk": true, "app": true,
		"main": true, "server": true, "tomcat": true,
	}
	return generics[name] || name == "." || name == ""
}

// extractJavaVersionFromArgs attempts to extract the Java version from
// command line arguments. Currently returns "unknown" as a placeholder.
func extractJavaVersionFromArgs(args []string) string {
	for i, arg := range args {
		if arg == "-version" && i > 0 {
			return "unknown"
		}
	}
	return "unknown"
}

// extractJavaAgentFromEnvValue extracts the -javaagent: path from an
// environment variable value (e.g. from JAVA_TOOL_OPTIONS).
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

// extractAgentJarPath strips any trailing =options from a -javaagent: path.
func extractAgentJarPath(agentArg string) string {
	if idx := strings.Index(agentArg, "="); idx != -1 {
		return agentArg[:idx]
	}
	return agentArg
}

// detectJavaAgentType classifies a Java agent JAR path as Middleware,
// OpenTelemetry, or Other based on filename patterns.
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
