package otelinject

import (
	"fmt"
	"log"
	"net/url"
	"runtime"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

const apiPathForAgentSetting = "api/v1/agent/public/setting/"

func ReportStatus(
	hostname string,
	apiKey string,
	urlForConfigCheck string,
	version string,
	infraPlatform string,
) error {
	u, err := url.Parse(urlForConfigCheck)
	if err != nil {
		return err
	}
	baseURL := u.JoinPath(apiPathForAgentSetting, apiKey, hostname)

	client, err := discovery.NewAgentAPIClient(
		discovery.AgentAPIClientConfig{
			BaseURL:       baseURL.String(),
			APIKey:        apiKey,
			Version:       "v1",
			InfraPlatform: infraPlatform,
			Hostname:      hostname,
		},
	)

	if err != nil {
		return fmt.Errorf("failed to create api client for injector: %w", err)
	}

	rawReportValue, err := discovery.GetAgentReportValue()
	if err != nil {
		return fmt.Errorf("failed to generate agent report value: %w", err)
	}

	// Fetch stored settings from the backend and carry over any instrument_this=true
	// flags the user set via the UI. Without this, every report cycle would reset all
	// instrument_this fields to false (the discovery default), silently undoing user choices.
	storedRaw, fetchErr := client.GetAgentHostSettings()
	if fetchErr != nil {
		log.Printf("warning: failed to fetch stored agent settings, instrument_this flags will not be preserved: %v", fetchErr)
	} else {
		storedSettings, parseErr := discovery.GetAutoInstrumentationSettings(storedRaw)
		if parseErr != nil {
			log.Printf("warning: failed to parse stored auto_instrumentation_settings, instrument_this flags will not be preserved: %v", parseErr)
		} else {
			osKey := runtime.GOOS
			if osConfig, ok := rawReportValue[osKey]; ok {
				osConfig.AutoInstrumentationSettings = discovery.ApplyStoredInstrumentThis(
					storedSettings,
					osConfig.AutoInstrumentationSettings,
				)
				rawReportValue[osKey] = osConfig
			}
		}
	}

	if err := client.ReportStatus(rawReportValue); err != nil {
		return fmt.Errorf("failed to send report: %w", err)
	}
	return nil
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
	rawReportValue, err := discovery.GetAgentReportValue()

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
	// ListSystemdServices builds full ServiceInfo structs to extract unit names.
	// Acceptable at current scale; revisit if discovery becomes expensive.
	services, err := ListSystemdServices()
	units := make([]string, 0, len(services))
	for _, s := range services {
		units = append(units, s.SystemdUnit)
	}
	return units, err
}
