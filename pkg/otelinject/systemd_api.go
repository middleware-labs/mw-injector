package otelinject

import (
	"fmt"
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

func ListServices() ([]ServiceInfo, error) {
	rawReportValue, err := discovery.GetAgentReportValue()
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	var services []ServiceInfo
	for _, setting := range rawReportValue[runtime.GOOS].AutoInstrumentationSettings {
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

	return services, nil
}

func ListUnits() ([]string, error) {
	services, err := ListServices()
	if err != nil {
		return nil, err
	}
	units := make([]string, 0, len(services))
	for _, s := range services {
		units = append(units, s.SystemdUnit)
	}
	return units, nil
}
