// php_handler.go implements the LanguageHandler interface for PHP processes.
// It handles detection of PHP-CLI, PHP-FPM, PHP-CGI, LiteSpeed, FrankenPHP,
// and mod_php (Apache with libphp.so) processes.
//
// Not detected: RoadRunner server (Go binary — PHP workers are detected as php-cli).
package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PHPHandler implements LanguageHandler for PHP processes.
type PHPHandler struct{}

// Lang returns LangPHP.
func (h *PHPHandler) Lang() Language { return LangPHP }

// Detect returns true if the process is a PHP process, identified by executable
// name matching the PHP binary pattern (php, php-fpm, php-cgi, with optional
// version suffixes like php8.2, php-fpm7.4) or LiteSpeed PHP (lsphp).
func (h *PHPHandler) Detect(proc *ProcessInfo) bool {
	if phpExecutableRegex.MatchString(strings.ToLower(proc.ExeName)) {
		return true
	}
	// Deep scan: Apache/httpd with mod_php (libphp.so loaded into process)
	if apacheExecutableRegex.MatchString(strings.ToLower(proc.ExeName)) {
		return hasLibPHP(proc.PID)
	}
	return false
}

// Enrich populates a Process struct with PHP-specific details.
// Returns nil if the process should be skipped (FPM master, ignored systemd unit).
func (h *PHPHandler) Enrich(info *ProcessInfo, opts DiscoveryOptions, detector *ContainerDetector) *Process {
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
			Language:       LangPHP,

			ServiceName:        cached.ServiceName,
			RuntimeName:        cached.ServiceType,
			RuntimeVersion:     cached.RuntimeVersion,
			RuntimeDescription: "PHP Runtime",

			HasAgent:          cached.HasAgent,
			IsMiddlewareAgent: cached.IsMiddlewareAgent,
			AgentPath:         cached.AgentPath,
			AgentType:         cached.AgentType,
			ContainerInfo:     cached.ContainerInfo,

			Details: map[string]any{
				DetailEntryPoint:          cached.EntryPoint,
				DetailProcessManager:      cached.ServiceType,
				DetailSystemdUnit:         cached.SystemdUnit,
				DetailExplicitServiceName: cached.ExplicitServiceName,
				DetailWorkingDirectory:    cached.WorkingDirectory,
				DetailPHPPoolName:         cached.PackageName,
				DetailIsFPM:              cached.ServiceType == "php-fpm",
			},
		}
	}

	// Slow path
	if isIgnoredSystemdUnit(pid) {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true})
		return nil
	}

	if isFPMMaster(info.CmdLine) {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true})
		return nil
	}

	status := readProcessStatus(pid)
	runtimeName := classifyPHPRuntime(info.ExeName, pid)

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
		Language:       LangPHP,

		RuntimeName:        runtimeName,
		RuntimeVersion:     "unknown",
		RuntimeDescription: "PHP Runtime",

		Details: make(map[string]any),
	}

	// Container detection
	if opts.IncludeContainerInfo {
		containerInfo, err := detector.IsProcessInContainer(pid)
		if err == nil && containerInfo.IsContainer {
			proc.ContainerInfo = containerInfo
		}
	}

	h.extractPHPInfo(proc, cmdArgs)

	// Skip artisan serve — it's Laravel's dev server wrapper that spawns a
	// child php -S process. Instrumenting it creates a confusing duplicate.
	if proc.DetailString(DetailEntryPoint) == "artisan:serve" {
		CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{Ignore: true})
		return nil
	}

	enrichCommonDetails(proc)
	h.extractServiceName(proc)
	h.detectInstrumentation(proc)

	// Populate cache
	CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{
		ServiceName:         proc.ServiceName,
		ServiceType:         proc.RuntimeName,
		RuntimeVersion:      proc.RuntimeVersion,
		EntryPoint:          proc.DetailString(DetailEntryPoint),
		HasAgent:            proc.HasAgent,
		IsMiddlewareAgent:   proc.IsMiddlewareAgent,
		AgentPath:           proc.AgentPath,
		AgentType:           proc.AgentType,
		ContainerInfo:       proc.ContainerInfo,
		Owner:               proc.Owner,
		SystemdUnit:         proc.DetailString(DetailSystemdUnit),
		ExplicitServiceName: proc.DetailString(DetailExplicitServiceName),
		WorkingDirectory:    proc.DetailString(DetailWorkingDirectory),
		PackageName:         proc.DetailString(DetailPHPPoolName),
	})

	return proc
}

// PassesFilter checks simple owner-based filtering for PHP processes.
func (h *PHPHandler) PassesFilter(proc *Process, filter ProcessFilter) bool {
	if filter.CurrentUserOnly {
		return proc.Owner == currentUser()
	}
	return true
}

// ToServiceSetting converts a PHP Process into a ServiceSetting for
// backend reporting.
func (h *PHPHandler) ToServiceSetting(proc *Process) *ServiceSetting {
	key := fmt.Sprintf("host-php-%s", sanitize(proc.ServiceName))
	isSystemd, unitname := CheckSystemdStatus(proc.PID)
	serviceType := "system"
	if isSystemd {
		serviceType = "systemd"
	}

	if proc.IsInContainer() {
		serviceType = "docker"
		if proc.ContainerInfo.ContainerID != "" && len(proc.ContainerInfo.ContainerID) >= 12 {
			key = fmt.Sprintf("container-php-%s", proc.ContainerInfo.ContainerID[:12])
		}
	}

	agentType := deriveAgentType(proc.HasAgent, proc.AgentPath, proc.IsMiddlewareAgent)

	return &ServiceSetting{
		PID:            proc.PID,
		ServiceName:    proc.ServiceName,
		Owner:          proc.Owner,
		Status:         proc.Status,
		Enabled:        true,
		ServiceType:    serviceType,
		Language:       "php",
		RuntimeVersion: proc.RuntimeVersion,

		HasAgent:          proc.HasAgent,
		IsMiddlewareAgent: proc.IsMiddlewareAgent,
		AgentType:         agentType,
		AgentPath:         proc.AgentPath,
		Instrumented:      proc.HasAgent,

		Key:            key,
		ProcessManager: proc.DetailString(DetailProcessManager),
		SystemdUnit:    unitname,
		Listeners:      proc.Listeners(),
		Fingerprint:    proc.Fingerprint(),
	}
}

// --- Private helpers ---

// phpExecutableRegex matches PHP binary names with optional version suffixes.
// Matches: php, php8.2, php82, php-fpm, php-fpm8.3, php-cgi, php-cgi7.4, lsphp, lsphp82, frankenphp
var phpExecutableRegex = regexp.MustCompile(`^(php(-cgi|-fpm)?[0-9.]*|lsphp[0-9.]*|frankenphp)$`)

// apacheExecutableRegex matches Apache/httpd binary names that could embed mod_php.
var apacheExecutableRegex = regexp.MustCompile(`^(apache2|httpd)$`)

// libphpRegex matches libphp shared objects in /proc/<pid>/maps.
// Examples: libphp.so, libphp7.4.so, libphp8.2.so, libphp82.so
var libphpRegex = regexp.MustCompile(`libphp[0-9.]*\.so`)

// phpFlagsWithArg lists PHP CLI flags that consume the next argument.
// Without this, "php -S 127.0.0.1:8000 app.php" would pick "127.0.0.1:8000"
// as the entry point instead of "app.php".
var phpFlagsWithArg = map[string]bool{
	"-S": true, "-t": true, "-c": true, "-d": true,
	"-z": true, "-B": true, "-R": true,
	"-F": true, "-E": true,
}

// phpScriptsWithSubcommand lists extensionless PHP entry points whose first
// positional argument is a subcommand that differentiates the workload
// (e.g. "artisan serve" vs "artisan queue:work").
var phpScriptsWithSubcommand = map[string]bool{
	"artisan": true,
	"console": true,
}

// classifyPHPRuntime determines the PHP runtime type from the executable name.
func classifyPHPRuntime(exeName string, pid int32) string {
	exeLower := strings.ToLower(exeName)

	if strings.Contains(exeLower, "fpm") {
		return "php-fpm"
	}
	if strings.Contains(exeLower, "cgi") {
		return "php-cgi"
	}
	if strings.HasPrefix(exeLower, "lsphp") {
		return "litespeed"
	}
	if exeLower == "frankenphp" {
		return "frankenphp"
	}
	if apacheExecutableRegex.MatchString(exeLower) && hasLibPHP(pid) {
		return "mod_php"
	}
	return "php-cli"
}

// isFPMMaster returns true if the cmdline indicates this is an FPM master process.
// FPM master: "php-fpm: master process (/etc/php/8.2/fpm/php-fpm.conf)"
// FPM worker: "php-fpm: pool www"
func isFPMMaster(cmdline string) bool {
	return strings.Contains(cmdline, "master process")
}

func (h *PHPHandler) extractPHPInfo(proc *Process, cmdArgs []string) {
	if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", proc.PID)); err == nil {
		proc.Details[DetailWorkingDirectory] = cwd
	}

	// Detect FPM pool name from cmdline: "php-fpm: pool www"
	if strings.Contains(proc.CommandLine, "pool ") {
		if poolName := extractFPMPoolName(proc.CommandLine); poolName != "" {
			proc.Details[DetailPHPPoolName] = poolName
			proc.Details[DetailIsFPM] = true
			proc.Details[DetailProcessManager] = "php-fpm"
		}
	}

	// Extract script entry point from command args.
	// PHP flags that consume the next argument must be skipped to avoid
	// misidentifying e.g. "127.0.0.1:8000" (from -S) as the entry point.
	skipNext := false
	for i, arg := range cmdArgs {
		if i == 0 {
			continue
		}
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if phpFlagsWithArg[arg] {
				skipNext = true
			}
			continue
		}

		// First positional arg that looks like a PHP script or extensionless entry point
		if strings.HasSuffix(arg, ".php") || strings.HasSuffix(arg, ".phtml") {
			proc.Details[DetailEntryPoint] = filepath.Base(arg)
			// Only update working directory from script path if it's NOT inside
			// a vendor/ dir — vendor paths are dependencies, not the project root.
			if !isVendorPath(arg) {
				if filepath.IsAbs(arg) {
					proc.Details[DetailWorkingDirectory] = filepath.Dir(arg)
				} else if cwd := proc.DetailString(DetailWorkingDirectory); cwd != "" {
					proc.Details[DetailWorkingDirectory] = filepath.Dir(filepath.Join(cwd, arg))
				}
			}
			break
		}

		// Extensionless positional arg (e.g. "artisan", "bin/console")
		baseName := filepath.Base(arg)
		if phpScriptsWithSubcommand[baseName] {
			if sub := nextPositionalArg(cmdArgs, i+1); sub != "" {
				baseName = baseName + ":" + sub
			}
		}
		proc.Details[DetailEntryPoint] = baseName
		break
	}

	// Read composer.json for project identity (host processes only)
	workDir := proc.DetailString(DetailWorkingDirectory)
	if workDir != "" && !proc.IsInContainer() {
		h.readComposerJson(proc, workDir)
	}
}

func (h *PHPHandler) readComposerJson(proc *Process, workDir string) {
	composerPath := filepath.Join(workDir, "composer.json")
	data, err := os.ReadFile(composerPath)
	if err != nil {
		return
	}

	var composer struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &composer) == nil && composer.Name != "" {
		proc.Details[DetailPackageName] = composer.Name
	}
}

func (h *PHPHandler) extractServiceName(proc *Process) {
	// Level 1: Explicit environment (OTEL_SERVICE_NAME, SERVICE_NAME)
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

	// Level 3: Systemd unit name
	if unit := proc.DetailString(DetailSystemdUnit); unit != "" {
		if name := cleanName(unit); name != "" {
			proc.ServiceName = name
			return
		}
	}

	// Level 4: FPM pool name (skip default "www" — not useful as identity)
	if poolName := proc.DetailString(DetailPHPPoolName); poolName != "" && poolName != "www" {
		if name := cleanName(poolName); name != "" {
			proc.ServiceName = name
			return
		}
	}

	// Level 5: Script filename
	if entryPoint := proc.DetailString(DetailEntryPoint); entryPoint != "" {
		if name := extractNameFromPHPScript(entryPoint); name != "" {
			proc.ServiceName = name
			return
		}
	}

	// Level 6: Composer package name ("vendor/package" → use package part)
	if pkgName := proc.DetailString(DetailPackageName); pkgName != "" {
		parts := strings.Split(pkgName, "/")
		name := parts[len(parts)-1]
		if cleaned := cleanName(name); cleaned != "" {
			proc.ServiceName = cleaned
			return
		}
	}

	// Level 7: Working directory
	if workDir := proc.DetailString(DetailWorkingDirectory); workDir != "" {
		if name := serviceNameFromWorkDir(workDir); name != "" {
			proc.ServiceName = name
			return
		}
	}

	proc.ServiceName = "php-service"
}

func (h *PHPHandler) detectInstrumentation(proc *Process) {
	// Check command line for OTel PHP extension loaded via -d flag
	if strings.Contains(proc.CommandLine, "extension=opentelemetry") ||
		strings.Contains(proc.CommandLine, "zend_extension=opentelemetry") {
		proc.HasAgent = true
		proc.AgentType = "opentelemetry"
		return
	}

	// Check environment variables
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", proc.PID))
	if err != nil {
		return
	}

	for env := range strings.SplitSeq(string(data), "\x00") {
		// OTel PHP SDK auto-instrumentation enabled
		if value, ok := strings.CutPrefix(env, "OTEL_PHP_AUTOLOAD_ENABLED="); ok {
			if strings.EqualFold(value, "true") || value == "1" {
				proc.HasAgent = true
				proc.AgentType = "opentelemetry"
				return
			}
		}
	}
}

// isVendorPath returns true if the path contains a vendor/ or node_modules/
// segment, indicating it's a dependency rather than project code.
func isVendorPath(path string) bool {
	return strings.Contains(path, "/vendor/") || strings.Contains(path, "/node_modules/")
}

// nextPositionalArg returns the next non-flag argument starting at index start,
// or "" if none found.
func nextPositionalArg(args []string, start int) string {
	for i := start; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return ""
}

// hasLibPHP checks /proc/<pid>/maps for a loaded libphp*.so shared object,
// indicating this process has PHP embedded via mod_php (Apache with libphp).
func hasLibPHP(pid int32) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return false
	}
	return libphpRegex.Match(data)
}

// extractFPMPoolName extracts the pool name from an FPM worker cmdline.
// Example: "php-fpm: pool www" → "www"
func extractFPMPoolName(cmdline string) string {
	_, after, found := strings.Cut(cmdline, "pool ")
	if !found {
		return ""
	}
	poolName := strings.TrimSpace(after)
	if spaceIdx := strings.IndexByte(poolName, ' '); spaceIdx != -1 {
		poolName = poolName[:spaceIdx]
	}
	return poolName
}

// extractNameFromPHPScript derives a service name from a PHP script filename,
// filtering out generic names like index.php, app.php.
func extractNameFromPHPScript(scriptName string) string {
	if scriptName == "" {
		return ""
	}

	baseName := strings.TrimSuffix(scriptName, filepath.Ext(scriptName))
	baseName = filepath.Base(baseName)

	// Split off subcommand before checking generics — "console:messenger:consume"
	// should not be filtered even though bare "console" is generic.
	baseForGenericCheck := baseName
	if idx := strings.IndexByte(baseForGenericCheck, ':'); idx != -1 {
		baseForGenericCheck = baseForGenericCheck[:idx]
	}

	genericPHPNames := map[string]bool{
		"index": true, "app": true, "main": true, "server": true, "start": true,
		"run": true, "php": true, "worker": true, "console": true, "cron": true,
	}

	if genericPHPNames[strings.ToLower(baseForGenericCheck)] {
		if baseForGenericCheck == baseName {
			return ""
		}
	}

	return cleanName(strings.ReplaceAll(baseName, ":", "-"))
}
