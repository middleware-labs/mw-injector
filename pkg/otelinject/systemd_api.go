package otelinject

import (
	"fmt"
	"net/url"
	"runtime"

	"github.com/k0kubun/pp"
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
	pp.Println(rawReportValue)
	return nil
}

func InstrumentUnit(unitName string, lang Language) error {
	dropIn, err := NewSystemdDropin(unitName)
	pp.Println("Dropin created: ", dropIn)
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

func ListUnits() ([]string, error) {
	rawReportValue, err := discovery.GetAgentReportValue()
	if err != nil {
		return nil, fmt.Errorf("failed to list units: %w ", err)
	}

	units := []string{}

	for _, setting := range rawReportValue[runtime.GOOS].AutoInstrumentationSettings {
		units = append(units, setting.SystemdUnit)
	}

	return units, nil
}
