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
