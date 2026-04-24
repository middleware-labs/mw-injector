// report.go builds the AgentReportValue sent to the Middleware backend. It
// discovers all processes, converts them to ServiceSettings via each handler's
// ToServiceSetting method, and assembles the final report payload.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
)

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
	Listeners           []Listener `json:"listeners,omitempty"`
	InstrumentationType string     `json:"instrumentation_type,omitempty"`
	Fingerprint         string     `json:"fingerprint,omitempty"`
}

// OSConfig represents the configuration and status for a specific OS (e.g., "linux").
type OSConfig struct {
	AgentRestartStatus          bool                      `json:"agent_restart_status"`
	AutoInstrumentationInit     bool                      `json:"auto_instrumentation_init"`
	AutoInstrumentationSettings map[string]ServiceSetting `json:"auto_instrumentation_settings"`
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
	// language's handler for the conversion.
	settings := map[string]ServiceSetting{}
	for lang, procs := range allProcs {
		handler := d.handlerRegistry.ForLanguage(lang)
		if handler == nil {
			continue
		}
		for _, proc := range procs {
			if ss := handler.ToServiceSetting(proc); ss != nil {
				settings[ss.Key] = *ss
			}
		}
	}

	osKey := runtime.GOOS
	reportValue := AgentReportValue{
		osKey: OSConfig{
			AgentRestartStatus:          false,
			AutoInstrumentationInit:     true,
			AutoInstrumentationSettings: settings,
		},
	}

	return reportValue, discoverErrs
}

// ApplyStoredInstrumentThis merges instrument_this flags from storedSettings into
// currentSettings. All current services are returned; instrument_this is set to true
// only when a stored entry with a matching (service_name, language) has InstrumentThis=true.
func ApplyStoredInstrumentThis(
	storedSettings map[string]ServiceSetting,
	currentSettings map[string]ServiceSetting,
) map[string]ServiceSetting {
	type serviceIdentity struct {
		ServiceName string
		Language    string
	}
	storedIndex := make(map[serviceIdentity]bool, len(storedSettings))
	for _, s := range storedSettings {
		if s.InstrumentThis {
			storedIndex[serviceIdentity{s.ServiceName, s.Language}] = true
		}
	}

	result := make(map[string]ServiceSetting, len(currentSettings))
	for k, current := range currentSettings {
		if storedIndex[serviceIdentity{current.ServiceName, current.Language}] {
			current.InstrumentThis = true
		}
		result[k] = current
	}
	return result
}

func FilterServices(services map[string]ServiceSetting, predicate func(ServiceSetting) bool) map[string]ServiceSetting {
	result := make(map[string]ServiceSetting)
	for k, v := range services {
		if predicate(v) {
			result[k] = v
		}
	}
	return result
}

func And(predicates ...func(ServiceSetting) bool) func(ServiceSetting) bool {
	return func(s ServiceSetting) bool {
		for _, p := range predicates {
			if !p(s) {
				return false
			}
		}
		return true
	}
}

func FilterInstrumentable(
	storedSettings map[string]ServiceSetting,
	currentSettings map[string]ServiceSetting,
) map[string]ServiceSetting {
	type serviceIdentity struct {
		ServiceName string
		Language    string
	}
	currentIndex := make(map[serviceIdentity]ServiceSetting, len(currentSettings))
	for _, current := range currentSettings {
		id := serviceIdentity{current.ServiceName, current.Language}
		currentIndex[id] = current
	}

	result := make(map[string]ServiceSetting)
	for _, stored := range storedSettings {
		if !stored.InstrumentThis {
			continue
		}

		id := serviceIdentity{stored.ServiceName, stored.Language}
		current, exists := currentIndex[id]
		if !exists {
			continue
		}

		current.InstrumentThis = true
		result[current.Key] = current
	}

	return result
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

	for _, line := range strings.Split(string(data), "\n") {
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
			if strings.HasSuffix(seg, ".service") && !strings.HasPrefix(seg, "user@") {
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
