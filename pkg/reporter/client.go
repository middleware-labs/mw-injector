package reporter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

const apiPath = "api/v1/agent/public/setting/"

// AgentAPIClient defines the contract for communicating with the MW backend.
type AgentAPIClient interface {
	Send(reportValue discovery.AgentReportValue) error
	FetchSettings() (map[string]interface{}, error)
}

// ClientConfig holds everything needed to construct an agentAPIClient.
type ClientConfig struct {
	BaseURL       string
	APIKey        string
	Hostname      string
	Version       string
	InfraPlatform string
	Timeout       time.Duration // optional, defaults to 10s
}

type agentAPIClient struct {
	baseURL       string
	apiKey        string
	hostname      string
	version       string
	infraPlatform string
	httpClient    *http.Client
}

// parseStoredSettings converts the raw backend GET response into a typed ServiceSetting map.
func parseStoredSettings(res map[string]any) (map[string]discovery.ServiceSetting, error) {
	if res == nil {
		return nil, fmt.Errorf("invalid response")
	}

	setting, ok := res["setting"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'setting' field")
	}

	config, ok := setting["config"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'config' field")
	}

	osConfig, ok := config[runtime.GOOS].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no config found for OS: %s", runtime.GOOS)
	}

	rawSettings, ok := osConfig["auto_instrumentation_settings"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'auto_instrumentation_settings' field")
	}

	result := make(map[string]discovery.ServiceSetting, len(rawSettings))
	for key, val := range rawSettings {
		entry, ok := val.(map[string]interface{})
		if !ok {
			continue
		}

		b, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal entry %q: %w", key, err)
		}

		var s discovery.ServiceSetting
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("failed to unmarshal entry %q into ServiceSetting: %w", key, err)
		}

		result[key] = s
	}

	return result, nil
}

// agentSettingPayload is the wire format for the backend POST request.
type agentSettingPayload struct {
	Value    string                            `json:"value"` // Base64-encoded AgentReportValue JSON
	MetaData map[string]interface{}            `json:"meta_data"`
	Config   map[string]map[string]interface{} `json:"config"`
}
