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

	"github.com/k0kubun/pp"
)

const apiPathForAgentHostSettings = "api/v1/agent/public/setting/"

type AgentAPIClient interface {
	ReportStatus(reportValue AgentReportValue) error
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
	// finalURL := u.JoinPath(apiPathForAgentHostSettings, c.apiKey, c.hostname).String()

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

	pp.Println("URL:", u)
	// 4. Build + execute HTTP POST
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

	// 5. Parse response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}
