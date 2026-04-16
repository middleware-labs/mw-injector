// systemd_api.go provides the high-level API for instrumenting systemd units.
// It includes InstrumentUnit (instruments a single unit by language),
// ListSystemdServices (discovers instrumentable units), and ReportStatus
// (builds a status report of all discoverable services).
package otelinject

import (
	"fmt"
	"log/slog"
	"runtime"

	"github.com/middleware-labs/java-injector/pkg/discovery"
	"github.com/middleware-labs/java-injector/pkg/reporter"
)

func ReportStatus(
	hostname string,
	apiKey string,
	urlForConfigCheck string,
	version string,
	infraPlatform string,
) error {
	return ReportStatusWithLogger(hostname, apiKey, urlForConfigCheck, version, infraPlatform, nil)
}

// ReportStatusWithLogger is like ReportStatus but threads an optional slog
// logger through the discovery pipeline so timing records are emitted.
// A nil logger disables logging.
func ReportStatusWithLogger(
	hostname string,
	apiKey string,
	urlForConfigCheck string,
	version string,
	infraPlatform string,
	logger *slog.Logger,
) error {
	r, err := reporter.New(hostname, apiKey, urlForConfigCheck, version, infraPlatform)
	if err != nil {
		return err
	}
	return r.SyncWithLogger(logger)
}

func InstrumentUnit(unitName string, lang Language) error {
	dropIn, err := NewSystemdDropin(unitName)
	if err != nil {
		return fmt.Errorf("failed to create systemd dropin: %w", err)
	}

	switch lang {
	case LanguageJava:
		if status := ValidateJavaAgent(""); !status.Ready {
			return fmt.Errorf("java agent is not ready, %v", status.Errors)
		}
		return dropIn.applySystemdDropIn()
	case LanguagePython:
		if status := ValidatePythonAgent(""); !status.Ready {
			return fmt.Errorf("python agent is not ready, %v", status.Errors)
		}
		return dropIn.applySystemdDropInPython()
	case LanguageNode:
		if status := ValidateNodeAgent(""); !status.Ready {
			return fmt.Errorf("node agent is not ready, %v", status.Errors)
		}
		return dropIn.applySystemdDropIn()
	default:
		return fmt.Errorf("unsupported language %s", lang)
	}
}

func UninstrumentUnit(unitName string) error {
	return removeSystemdDropIn(unitName)
}

// ServiceInfo holds display-relevant fields for a discovered service.
type ServiceInfo struct {
	Name         string
	Language     string
	PID          int32
	Owner        string
	Status       string
	Instrumented bool
	SystemdUnit  string
	AgentType    string
}

// ListSystemdServices returns a ServiceInfo entry for every discovered process
// that is managed by a systemd unit. It tolerates partial discovery errors
// (e.g. Java found but Python failed) — valid results are returned alongside
// any non-nil error.
func ListSystemdServices() ([]ServiceInfo, error) {
	return ListSystemdServicesWithLogger(nil)
}

// ListSystemdServicesWithLogger is like ListSystemdServices but threads an
// optional slog logger through the discovery pipeline so timing records are
// emitted. A nil logger disables logging.
func ListSystemdServicesWithLogger(logger *slog.Logger) ([]ServiceInfo, error) {
	rawReportValue, err := discovery.GetAgentReportValueWithLogger(logger)

	osConfig, ok := rawReportValue[runtime.GOOS]
	if !ok {
		return nil, fmt.Errorf("no agent host-setting was found for the %s", runtime.GOOS)
	}

	var services []ServiceInfo
	for _, setting := range osConfig.AutoInstrumentationSettings {
		if setting.SystemdUnit == "" {
			continue
		}
		services = append(services, ServiceInfo{
			Name:         setting.ServiceName,
			Language:     setting.Language,
			PID:          setting.PID,
			Owner:        setting.Owner,
			Status:       setting.Status,
			Instrumented: setting.Instrumented,
			SystemdUnit:  setting.SystemdUnit,
			AgentType:    setting.AgentType,
		})
	}

	return services, err
}

// ListUnits returns the systemd unit names for all discovered systemd services.
// It delegates to ListSystemdServices; see that function for error semantics.
func ListUnits() ([]string, error) {
	services, err := ListSystemdServices()
	units := make([]string, 0, len(services))
	for _, s := range services {
		units = append(units, s.SystemdUnit)
	}
	return units, err
}
