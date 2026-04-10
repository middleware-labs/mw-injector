package reporter

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

func newClient(cfg ClientConfig) (AgentAPIClient, error) {
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

// Send marshals, base64-encodes, and POSTs the report snapshot to the MW backend.
func (c *agentAPIClient) Send(reportValue discovery.AgentReportValue) error {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}

	rawBytes, err := json.Marshal(reportValue)
	if err != nil {
		return fmt.Errorf("failed to marshal report value: %w", err)
	}

	payload := agentSettingPayload{
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

	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST request failed for %s: %w", u.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// FetchSettings GETs the current stored settings from the MW backend.
func (c *agentAPIClient) FetchSettings() (map[string]interface{}, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	q := u.Query()
	q.Set("agent_type", "host")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET request failed for %s: %w", u.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// parseStoredSettings converts the raw backend GET response into a typed ServiceSetting map.
func parseStoredSettings(res map[string]interface{}) (map[string]discovery.ServiceSetting, error) {
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
