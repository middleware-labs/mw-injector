// report.go builds the AgentReportValue sent to the Middleware backend. It
// discovers all processes, converts them to ServiceSettings via each handler's
// ToServiceSetting method, and assembles the final report payload.
package discovery

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strings"
)

// ReportInstanceInfo holds per-PID details for an individual process instance.
// Separate from otelinject.InstanceInfo to avoid a circular import.
type ReportInstanceInfo struct {
	PID       int32      `json:"pid"`
	Owner     string     `json:"owner"`
	Status    string     `json:"status"`
	Listeners []Listener `json:"listeners,omitempty"`
}

// ServiceSetting represents the detailed status for a single service/process.
type ServiceSetting struct {
	PID                 int32      `json:"pid"`
	ServiceName         string     `json:"service_name"`
	Owner               string     `json:"owner"`
	Status              string     `json:"status"`
	Enabled             bool       `json:"enabled"`
	ServiceType         string     `json:"service_type"`
	Language            string     `json:"language"`
	RuntimeVersion      string     `json:"runtime_version"`
	SystemdUnit         string     `json:"systemd_unit,omitempty"`
	JarFile             string     `json:"jar_file,omitempty"`
	MainClass           string     `json:"main_class,omitempty"`
	HasAgent            bool       `json:"has_agent"`
	IsMiddlewareAgent   bool       `json:"is_middleware_agent"`
	AgentType           string     `json:"agent_type,omitempty"`
	AgentPath           string     `json:"agent_path,omitempty"`
	ConfigPath          string     `json:"config_path,omitempty"`
	Instrumented        bool       `json:"instrumented"`
	Key                 string     `json:"key"`
	InstrumentThis      bool       `json:"instrument_this"` // I want this to default to false.
	ProcessManager      string     `json:"process_manager,omitempty"`
	Listeners           []Listener `json:"listeners,omitempty"` // ports
	InstrumentationType string     `json:"instrumentation_type,omitempty"`
	// Mode the user picked on the Linux agent screen ("obi" or "otel-injector").
	InstrumentationMode string     `json:"instrumentation_mode,omitempty"`
	Fingerprint         string             `json:"fingerprint,omitempty"`
	IntegrationType     string             `json:"integration_type,omitempty"`
	Instances           []ReportInstanceInfo `json:"instances,omitempty"`
}

// IntegrationInstanceInfo holds per-PID details for a detected integration instance.
type IntegrationInstanceInfo struct {
	PID    int32  `json:"pid"`
	Owner  string `json:"owner"`
	Status string `json:"status"`
	Ports  []int  `json:"ports,omitempty"`
}

// IntegrationSetting represents a detected infrastructure integration
// (Redis, MySQL, PostgreSQL, etc.) reported to the backend.
type IntegrationSetting struct {
	IntegrationType string                    `json:"integration_type"`
	ServiceName     string                    `json:"service_name"`
	ServiceType     string                    `json:"service_type"`
	Ports           []int                     `json:"ports,omitempty"`
	Instances       []IntegrationInstanceInfo  `json:"instances,omitempty"`
}

// OSConfig represents the configuration and status for a specific OS (e.g., "linux").
type OSConfig struct {
	AutoInstrumentationInit     bool                             `json:"auto_instrumentation_init"`
	AutoInstrumentationSettings map[string]ServiceSetting        `json:"auto_instrumentation_settings"`
	Integrations                []IntegrationSetting             `json:"integrations,omitempty"`
}

// AgentReportValue is the root structure for the 'value' field's JSON content.
type AgentReportValue map[string]OSConfig

// GetAgentReportValue discovers all processes and converts them to an
// AgentReportValue for backend reporting. Uses the handler registry to
// loop over all supported languages.
//
// Use GetAgentReportValueWithLogger to receive structured timing logs.
func GetAgentReportValue() (AgentReportValue, error) {
	return GetAgentReportValueWithLogger(nil)
}

// GetAgentReportValueWithLogger is like GetAgentReportValue but emits
// structured timing/diagnostic logs via the supplied slog logger. A nil
// logger disables logging.
func GetAgentReportValueWithLogger(logger *slog.Logger) (AgentReportValue, error) {
	ctx := context.Background()
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true
	opts.Logger = logger

	d, err := NewDiscovererWithOptions(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create discoverer: %w", err)
	}
	defer d.Close()

	allProcs, discoverErrs := d.DiscoverAll(ctx)

	// Garbage collection — remove stale cache entries
	PruneProcessCache()
	PruneContainerNameCache()

	// Convert all discovered processes to ServiceSettings using each
	// language's handler for the conversion. Key by fingerprint to
	// accumulate multiple instances of the same workload class.
	settings := map[string]ServiceSetting{}
	for lang, procs := range allProcs {
		handler := d.handlerRegistry.ForLanguage(lang)
		if handler == nil {
			continue
		}
		for _, proc := range procs {
			ss := handler.ToServiceSetting(proc)
			if ss == nil {
				continue
			}
			mapKey := ss.Fingerprint
			if mapKey == "" {
				mapKey = ss.Key
			}
			inst := ReportInstanceInfo{
				PID:       ss.PID,
				Owner:     ss.Owner,
				Status:    ss.Status,
				Listeners: ss.Listeners,
			}
			if existing, ok := settings[mapKey]; ok {
				existing.Instances = append(existing.Instances, inst)
				existing.Listeners = mergeListeners(existing.Listeners, ss.Listeners)
				existing.HasAgent = existing.HasAgent || ss.HasAgent
				existing.Instrumented = existing.Instrumented || ss.Instrumented
				settings[mapKey] = existing
			} else {
				ss.Instances = []ReportInstanceInfo{inst}
				settings[mapKey] = *ss
			}
		}
	}

	// Discover infrastructure integrations (Redis, MySQL, etc.) that are not
	// language-classified processes.
	skipPIDs := make(map[int32]struct{})
	for _, procs := range allProcs {
		for _, proc := range procs {
			skipPIDs[proc.PID] = struct{}{}
		}
	}

	log.Printf("discovering infrastructure integrations (skipping %d language-classified PIDs)", len(skipPIDs))
	integrationProcs, integrationErr := d.DiscoverIntegrations(ctx, skipPIDs)
	if integrationErr != nil {
		log.Printf("warning: integration discovery failed: %v", integrationErr)
		if discoverErrs == nil {
			discoverErrs = integrationErr
		}
	} else {
		log.Printf("integration discovery found %d processes", len(integrationProcs))
	}

	integrations := buildIntegrationSettings(integrationProcs)

	osKey := runtime.GOOS
	reportValue := AgentReportValue{
		osKey: OSConfig{
			AutoInstrumentationInit:     true,
			AutoInstrumentationSettings: settings,
			Integrations:                integrations,
		},
	}

	return reportValue, discoverErrs
}

// ApplyStoredInstrumentThis carries the user's choices (instrument_this and
// instrumentation_mode) from storedSettings into currentSettings, matched on
// (service_name, language). All current services are returned.
func ApplyStoredInstrumentThis(
	storedSettings map[string]ServiceSetting,
	currentSettings map[string]ServiceSetting,
) map[string]ServiceSetting {
	type serviceIdentity struct {
		ServiceName string
		Language    string
	}
	storedTicked := make(map[serviceIdentity]bool, len(storedSettings))
	storedMode := make(map[serviceIdentity]string, len(storedSettings))
	for _, s := range storedSettings {
		id := serviceIdentity{s.ServiceName, s.Language}
		if s.InstrumentThis {
			storedTicked[id] = true
		}
		if s.InstrumentationMode != "" {
			storedMode[id] = s.InstrumentationMode
		}
	}

	result := make(map[string]ServiceSetting, len(currentSettings))
	for k, current := range currentSettings {
		id := serviceIdentity{current.ServiceName, current.Language}
		if storedTicked[id] {
			current.InstrumentThis = true
		}
		// The same key (fingerprint) is the exact service; fall back to the identity.
		if same, ok := storedSettings[k]; ok && same.InstrumentationMode != "" && id == (serviceIdentity{same.ServiceName, same.Language}) {
			current.InstrumentationMode = same.InstrumentationMode
		} else if mode := storedMode[id]; mode != "" {
			current.InstrumentationMode = mode
		}
		result[k] = current
	}
	return result
}

func buildIntegrationSettings(procs []*Process) []IntegrationSetting {
	if len(procs) == 0 {
		return nil
	}

	type group struct {
		integrationType string
		serviceName     string
		serviceType     string
		ports           map[int]struct{}
		instances       []IntegrationInstanceInfo
	}

	groups := make(map[string]*group)
	var order []string

	for _, proc := range procs {
		itype := proc.IntegrationType()
		if itype == "" {
			continue
		}

		g, exists := groups[itype]
		if !exists {
			serviceType := "standalone"
			if unit, _ := parseCgroupUnitName(proc.PID); unit != "" {
				serviceType = "systemd"
			} else if proc.IsInContainer() {
				serviceType = "docker"
			}
			g = &group{
				integrationType: itype,
				serviceName:     proc.ServiceName,
				serviceType:     serviceType,
				ports:           make(map[int]struct{}),
			}
			groups[itype] = g
			order = append(order, itype)
		}

		inst := IntegrationInstanceInfo{
			PID:    proc.PID,
			Owner:  proc.Owner,
			Status: proc.Status,
		}
		for _, l := range proc.Listeners() {
			p := int(l.Port)
			inst.Ports = append(inst.Ports, p)
			g.ports[p] = struct{}{}
		}
		sort.Ints(inst.Ports)
		g.instances = append(g.instances, inst)
	}

	result := make([]IntegrationSetting, 0, len(groups))
	for _, key := range order {
		g := groups[key]
		ports := make([]int, 0, len(g.ports))
		for p := range g.ports {
			ports = append(ports, p)
		}
		sort.Ints(ports)
		result = append(result, IntegrationSetting{
			IntegrationType: g.integrationType,
			ServiceName:     g.serviceName,
			ServiceType:     g.serviceType,
			Ports:           ports,
			Instances:       g.instances,
		})
		log.Printf("integration detected: type=%s service=%s serviceType=%s ports=%v instances=%d",
			g.integrationType, g.serviceName, g.serviceType, ports, len(g.instances))
	}
	log.Printf("built %d integration settings for report", len(result))
	return result
}

func mergeListeners(a, b []Listener) []Listener {
	if len(b) == 0 {
		return a
	}
	seen := make(map[uint16]struct{}, len(a))
	for _, l := range a {
		seen[l.Port] = struct{}{}
	}
	for _, l := range b {
		if _, ok := seen[l.Port]; !ok {
			a = append(a, l)
			seen[l.Port] = struct{}{}
		}
	}
	return a
}

func deriveAgentType(hasAgent bool, agentPath string, isMiddleware bool) string {
	if !hasAgent {
		return ""
	}
	if strings.Contains(agentPath, "libotelinject.so") {
		return "otel-injector"
	}
	if isMiddleware {
		return "middleware"
	}
	return "opentelemetry"
}

func sanitize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	result := strings.Join(strings.FieldsFunc(b.String(), func(r rune) bool { return r == '-' }), "-")
	return strings.Trim(result, "-")
}

// parseCgroupUnitName reads /proc/<pid>/cgroup and returns the innermost
// non-user systemd unit name.
func parseCgroupUnitName(pid int32) (string, bool) {
	path := fmt.Sprintf("/proc/%d/cgroup", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return parseCgroupUnitContent(string(data))
}

// parseCgroupUnitContent parses raw cgroup file content to extract the systemd unit name.
func parseCgroupUnitContent(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, ":name=systemd:") && !strings.HasPrefix(line, "0::") {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		segments := strings.Split(parts[2], "/")
		for i := len(segments) - 1; i >= 0; i-- {
			seg := segments[i]
			if strings.HasSuffix(seg, ".service") && !strings.HasPrefix(seg, "user@") && !strings.HasPrefix(seg, "app-") {
				return strings.TrimSuffix(seg, ".service"), true
			}
		}
	}
	return "", false
}

func CheckSystemdStatus(pid int32) (bool, string) {
	name, found := parseCgroupUnitName(pid)
	return found, name
}
