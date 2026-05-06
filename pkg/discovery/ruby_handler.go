package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RubyHandler implements LanguageHandler for Ruby processes.
type RubyHandler struct{}

func (h *RubyHandler) Lang() Language { return LangRuby }

// Detect returns true if the process is a Ruby process, identified by
// executable name (ruby, jruby), Ruby-based binaries (rails, puma,
// sidekiq, unicorn, rake, bundler), or .rb file references in cmdline.
func (h *RubyHandler) Detect(proc *ProcessInfo) bool {
	exeLower := strings.ToLower(proc.ExeName)

	if rubyExecutables[exeLower] {
		return true
	}

	if rubyBinaries[exeLower] {
		return true
	}

	cmdLower := strings.ToLower(proc.CmdLine)
	for _, pattern := range rubyCmdPatterns {
		if strings.Contains(cmdLower, pattern) {
			return true
		}
	}

	if strings.Contains(cmdLower, ".rb") {
		return true
	}

	return false
}

func (h *RubyHandler) Enrich(info *ProcessInfo, opts DiscoveryOptions, detector *ContainerDetector) *Process {
	pid := info.PID
	cmdArgs := info.CmdArgs

	owner := readProcessOwner(pid)
	createTime := readProcessCreateTime(pid)
	alignedTime := (createTime / 1000) * 1000

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
			Language:       LangRuby,

			ServiceName:        cached.ServiceName,
			RuntimeName:        "ruby",
			RuntimeVersion:     cached.RuntimeVersion,
			RuntimeDescription: "Ruby Interpreter",

			HasAgent:          cached.HasAgent,
			IsMiddlewareAgent: cached.IsMiddlewareAgent,
			AgentPath:         cached.AgentPath,
			ContainerInfo:     cached.ContainerInfo,

			Details: map[string]any{
				DetailEntryPoint:          cached.EntryPoint,
				DetailProcessManager:      cached.ServiceType,
				DetailSystemdUnit:         cached.SystemdUnit,
				DetailExplicitServiceName: cached.ExplicitServiceName,
				DetailWorkingDirectory:    cached.WorkingDirectory,
			},
		}
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
		CommandLine:    info.CmdLine,
		CommandArgs:    cmdArgs,
		Owner:          owner,
		CreateTime:     timeFromMillis(createTime),
		Status:         status,
		Language:       LangRuby,

		RuntimeName:        "ruby",
		RuntimeDescription: "Ruby Interpreter",

		Details: make(map[string]any),
	}

	if opts.IncludeContainerInfo || opts.ExcludeContainers {
		containerInfo, err := detector.IsProcessInContainer(pid)
		if err == nil {
			proc.ContainerInfo = containerInfo

			if opts.ExcludeContainers && containerInfo.IsContainer {
				return nil
			}
		}
	}

	h.extractRubyInfo(proc, cmdArgs)
	enrichCommonDetails(proc)
	h.extractServiceName(proc, cmdArgs)
	h.detectProcessManager(proc, cmdArgs)
	h.detectInstrumentation(proc)

	CacheProcessMetadata(pid, alignedTime, ProcessCacheEntry{
		ServiceName:         proc.ServiceName,
		ServiceType:         proc.DetailString(DetailProcessManager),
		RuntimeVersion:      proc.RuntimeVersion,
		EntryPoint:          proc.DetailString(DetailEntryPoint),
		HasAgent:            proc.HasAgent,
		IsMiddlewareAgent:   proc.IsMiddlewareAgent,
		AgentPath:           proc.AgentPath,
		ContainerInfo:       proc.ContainerInfo,
		Owner:               proc.Owner,
		SystemdUnit:         proc.DetailString(DetailSystemdUnit),
		ExplicitServiceName: proc.DetailString(DetailExplicitServiceName),
		WorkingDirectory:    proc.DetailString(DetailWorkingDirectory),
	})

	return proc
}

func (h *RubyHandler) PassesFilter(proc *Process, filter ProcessFilter) bool {
	if filter.CurrentUserOnly {
		return proc.Owner == currentUser()
	}
	return true
}

func (h *RubyHandler) ToServiceSetting(proc *Process) *ServiceSetting {
	key := fmt.Sprintf("host-ruby-%s", sanitize(proc.ServiceName))
	isSystemd, unitname := CheckSystemdStatus(proc.PID)
	serviceType := "system"
	if isSystemd {
		serviceType = "systemd"
	}

	serviceName := proc.ServiceName

	if proc.IsInContainer() {
		serviceType = "docker"
		if proc.ContainerInfo.ContainerID != "" && len(proc.ContainerInfo.ContainerID) >= 12 {
			key = fmt.Sprintf("container-ruby-%s", proc.ContainerInfo.ContainerID[:12])
			serviceName = proc.ContainerInfo.ContainerName
		}
	}

	agentType := deriveAgentType(proc.HasAgent, proc.AgentPath, proc.IsMiddlewareAgent)

	return &ServiceSetting{
		PID:               proc.PID,
		ServiceName:       serviceName,
		Owner:             proc.Owner,
		Status:            proc.Status,
		Enabled:           true,
		ServiceType:       serviceType,
		Language:          "ruby",
		RuntimeVersion:    proc.RuntimeVersion,
		HasAgent:          proc.HasAgent,
		IsMiddlewareAgent: proc.IsMiddlewareAgent,
		AgentType:         agentType,
		AgentPath:         proc.AgentPath,
		Instrumented:      proc.HasAgent,
		Key:               key,
		ProcessManager:    proc.DetailString(DetailProcessManager),
		SystemdUnit:       unitname,
		Listeners:         proc.Listeners(),
		Fingerprint:       proc.Fingerprint(),
	}
}

// --- Private helpers ---

func (h *RubyHandler) extractRubyInfo(proc *Process, cmdArgs []string) {
	if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", proc.PID)); err == nil {
		proc.Details[DetailWorkingDirectory] = cwd
	}

	// Extract Ruby version from exe path (e.g. /usr/bin/ruby3.2 → "3.2")
	exeBase := filepath.Base(proc.ExecutablePath)
	if strings.HasPrefix(exeBase, "ruby") {
		ver := strings.TrimPrefix(exeBase, "ruby")
		if ver != "" {
			proc.RuntimeVersion = ver
		}
	}

	for i, arg := range cmdArgs {
		if i == 0 {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}

		argBase := filepath.Base(arg)
		if rubyBinaries[strings.ToLower(argBase)] {
			continue
		}

		proc.Details[DetailEntryPoint] = filepath.Base(arg)
		return
	}
}

// extractServiceName determines the service name for a Ruby process.
// Priority: 1. OTEL_SERVICE_NAME/SERVICE_NAME env → 2. Container name →
// 3. Systemd unit → 4. Gemfile app name → 5. Entry point basename →
// 6. Working directory → 7. "ruby-service"
func (h *RubyHandler) extractServiceName(proc *Process, cmdArgs []string) {
	if name := extractServiceNameFromEnviron(proc.PID); name != "" {
		proc.ServiceName = cleanName(name)
		if proc.ServiceName != "" {
			return
		}
	}

	if proc.IsInContainer() && proc.ContainerInfo.ContainerName != "" {
		proc.ServiceName = cleanName(proc.ContainerInfo.ContainerName)
		if proc.ServiceName != "" {
			return
		}
	}

	if unitName := extractSystemdUnit(proc.PID); unitName != "" {
		proc.ServiceName = cleanName(unitName)
		if proc.ServiceName != "" {
			return
		}
	}

	// Ruby framework binaries (puma, sidekiq, unicorn) have meaningful exe names
	if rubyBinaries[strings.ToLower(proc.ExecutableName)] {
		if name := cleanName(proc.ExecutableName); name != "" {
			proc.ServiceName = name
			return
		}
	}

	// Try Gemfile-based app name from working directory
	if dir := proc.DetailString(DetailWorkingDirectory); dir != "" {
		if name := rubyAppNameFromGemfile(dir); name != "" {
			proc.ServiceName = name
			return
		}
	}

	// Rails app name from cmdline (e.g. "rails server" → use working dir name)
	for _, arg := range cmdArgs {
		if strings.Contains(arg, "config.ru") || strings.Contains(arg, "rails") {
			if dir := proc.DetailString(DetailWorkingDirectory); dir != "" {
				if name := serviceNameFromWorkDir(dir); name != "" {
					proc.ServiceName = name
					return
				}
			}
		}
	}

	if ep := proc.DetailString(DetailEntryPoint); ep != "" {
		base := strings.TrimSuffix(filepath.Base(ep), ".rb")
		if name := cleanName(base); name != "" {
			proc.ServiceName = name
			return
		}
	}

	if dir := proc.DetailString(DetailWorkingDirectory); dir != "" {
		if name := serviceNameFromWorkDir(dir); name != "" {
			proc.ServiceName = name
			return
		}
	}

	proc.ServiceName = "ruby-service"
}

func (h *RubyHandler) detectProcessManager(proc *Process, cmdArgs []string) {
	cmdlineLower := strings.ToLower(strings.Join(cmdArgs, " "))

	if strings.Contains(cmdlineLower, "puma") {
		proc.Details[DetailProcessManager] = "puma"
	} else if strings.Contains(cmdlineLower, "unicorn") {
		proc.Details[DetailProcessManager] = "unicorn"
	} else if strings.Contains(cmdlineLower, "sidekiq") {
		proc.Details[DetailProcessManager] = "sidekiq"
	} else if strings.Contains(cmdlineLower, "passenger") {
		proc.Details[DetailProcessManager] = "passenger"
	}
}

func (h *RubyHandler) detectInstrumentation(proc *Process) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", proc.PID))
	if err != nil {
		return
	}

	env := string(data)

	// OpenTelemetry Ruby SDK sets OTEL_TRACES_EXPORTER or is loaded via RUBYOPT
	if strings.Contains(env, "OTEL_TRACES_EXPORTER=") || strings.Contains(env, "opentelemetry") {
		proc.HasAgent = true
		proc.AgentType = "opentelemetry"
	}

	for _, e := range strings.Split(env, "\x00") {
		if strings.HasPrefix(e, "LD_PRELOAD=") {
			ldPreload := strings.TrimPrefix(e, "LD_PRELOAD=")
			if path := extractLibOtelInjectPath(ldPreload); path != "" {
				proc.HasAgent = true
				proc.AgentPath = path
				proc.AgentType = "otel-injector"
				if strings.Contains(path, "middleware") {
					proc.IsMiddlewareAgent = true
				}
			}
		}
	}
}

// rubyAppNameFromGemfile attempts to read a Gemfile in the working directory
// and infer the app name. If there's no Gemfile, returns "".
// We don't parse the Gemfile itself — just use the directory name.
func rubyAppNameFromGemfile(dir string) string {
	gemfilePath := filepath.Join(dir, "Gemfile")
	if _, err := os.Stat(gemfilePath); err != nil {
		return ""
	}
	return cleanName(filepath.Base(dir))
}

// --- Ruby-specific lookup tables ---

var rubyExecutables = map[string]bool{
	"ruby":  true,
	"jruby": true,
	"truffleruby": true,
}

var rubyBinaries = map[string]bool{
	"rails":     true,
	"rake":      true,
	"puma":      true,
	"sidekiq":   true,
	"unicorn":   true,
	"passenger": true,
	"bundler":   true,
	"bundle":    true,
	"resque":    true,
	"thin":      true,
	"rackup":    true,
}

var rubyCmdPatterns = []string{
	"ruby ",
	"rails ",
	"puma ",
	"sidekiq ",
	"unicorn ",
	"bundle exec",
	"passenger ",
	"rackup ",
}
