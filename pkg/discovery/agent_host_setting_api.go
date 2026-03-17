package discovery

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"time"
)

const apiPathForAgentHostSettings = "api/v1/agent/public/setting/"

type AgentAPIClient interface {
	ReportStatus(reportValue AgentReportValue) error
	GetAgentHostSettings() (map[string]interface{}, error)
}

type agentAPIClient struct {
	baseURL       string
	apiKey        string
	hostname      string
	version       string
	infraPlatform string
	httpClient    *http.Client
}

// AgentAPIClientConfig holds everything needed to construct an agentAPIClient.
type AgentAPIClientConfig struct {
	BaseURL       string
	APIKey        string
	Hostname      string
	Version       string
	InfraPlatform string
	Timeout       time.Duration // optional, defaults to 10s
}

func NewAgentAPIClient(cfg AgentAPIClientConfig) (AgentAPIClient, error) {
	if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Hostname == "" {
		return nil, fmt.Errorf("BaseURL, APIKey, and Hostname are required")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	return &agentAPIClient{
		baseURL:       cfg.BaseURL,
		apiKey:        cfg.APIKey,
		hostname:      cfg.Hostname,
		version:       cfg.Version,
		infraPlatform: cfg.InfraPlatform,
		httpClient:    &http.Client{Timeout: timeout},
	}, nil
}

// ReportStatus implements AgentAPIClient.
func (c *agentAPIClient) ReportStatus(reportValue AgentReportValue) error {
	// 1. Build URL
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}

	// 2. Marshal + base64 encode the report value
	rawBytes, err := json.Marshal(reportValue)
	if err != nil {
		return fmt.Errorf("failed to marshal report value: %w", err)
	}

	// 3. Build payload
	payload := AgentSettingPayload{
		Value: base64.StdEncoding.EncodeToString(rawBytes),
		MetaData: map[string]interface{}{
			"agent_version":  c.version,
			"platform":       runtime.GOOS,
			"infra_platform": c.infraPlatform,
		},
		Config: nil,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request payload: %w", err)
	}

	// 4. Build + execute HTTP POST
	req, err := http.NewRequest(
		http.MethodPost,
		u.String(),
		bytes.NewBuffer(payloadBytes),
	)
	if err != nil {
		return fmt.Errorf("failed to create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST request failed for %s: %w", u.String(), err)
	}
	defer resp.Body.Close()

	// 5. Parse response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func (c *agentAPIClient) GetAgentHostSettings() (map[string]interface{}, error) {
	// 1. Build URL with query param
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	q := u.Query()
	q.Set("agent_type", "host")
	u.RawQuery = q.Encode()

	// 2. Build GET request
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	// 3. Execute
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET request failed for %s: %w", u.String(), err)
	}

	defer resp.Body.Close()

	// 4. Check response
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// 5. Decode response

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil

}

func GetAutoInstrumentationSettings(res map[string]interface{}) (map[string]ServiceSetting, error) {
	if res == nil {
		return nil, fmt.Errorf("invalid response")
	}
	setting, ok := res["setting"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'setting' field")
	}

	config, ok := setting["config"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'config' field")
	}

	osConfig, ok := config[runtime.GOOS].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no config found for OS: %s", runtime.GOOS)
	}

	rawSettings, ok := osConfig["auto_instrumentation_settings"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'auto_instrumentation_settings' field")
	}

	// Convert each entry into a ServiceSetting
	result := make(map[string]ServiceSetting, len(rawSettings))
	for key, val := range rawSettings {
		entry, ok := val.(map[string]interface{})
		if !ok {
			continue
		}

		// Re-marshal and unmarshal into ServiceSetting for clean type conversion
		b, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal entry %q: %w", key, err)
		}

		var s ServiceSetting
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("failed to unmarshal entry %q into ServiceSetting: %w", key, err)
		}

		result[key] = s
	}

	return result, nil
}
