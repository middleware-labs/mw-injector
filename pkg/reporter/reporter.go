package reporter

import (
	"fmt"
	"log"
	"log/slog"
	"net/url"
	"runtime"

	"github.com/k0kubun/pp"
	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// Reporter orchestrates the full discovery → merge → send cycle.
type Reporter struct {
	client AgentAPIClient
}

// New constructs a Reporter wired up to the MW backend at urlForConfigCheck.
func New(hostname, apiKey, urlForConfigCheck, version, infraPlatform string) (*Reporter, error) {
	u, err := url.Parse(urlForConfigCheck)
	if err != nil {
		return nil, err
	}
	baseURL := u.JoinPath(apiPath, apiKey, hostname)

	client, err := newClient(ClientConfig{
		BaseURL:       baseURL.String(),
		APIKey:        apiKey,
		Version:       version,
		InfraPlatform: infraPlatform,
		Hostname:      hostname,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create reporter client: %w", err)
	}

	return &Reporter{client: client}, nil
}

// Sync discovers all running processes, preserves user-configured instrument_this
// flags fetched from the backend, and sends the merged snapshot.
func (r *Reporter) Sync() error {
	return r.SyncWithLogger(nil)
}

// SyncWithLogger is like Sync but threads an optional slog logger through
// the discovery pipeline so timing records are emitted. A nil logger
// disables discovery-level logging.
func (r *Reporter) SyncWithLogger(logger *slog.Logger) error {
	snapshot, err := discovery.GetAgentReportValueWithLogger(logger)
	if err != nil {
		return fmt.Errorf("failed to build host snapshot: %w", err)
	}

	// Fetch stored settings to carry over any instrument_this=true flags the
	// user set via the UI. Without this, every cycle would reset them to false.
	storedRaw, fetchErr := r.client.FetchSettings()
	if fetchErr != nil {
		log.Printf("warning: failed to fetch stored agent settings, instrument_this flags will not be preserved: %v", fetchErr)
	} else {
		storedSettings, parseErr := parseStoredSettings(storedRaw)
		if parseErr != nil {
			log.Printf("warning: failed to parse stored auto_instrumentation_settings, instrument_this flags will not be preserved: %v", parseErr)
		} else {
			osKey := runtime.GOOS
			if osConfig, ok := snapshot[osKey]; ok {
				osConfig.AutoInstrumentationSettings = discovery.ApplyStoredInstrumentThis(
					storedSettings,
					osConfig.AutoInstrumentationSettings,
				)
				snapshot[osKey] = osConfig
			}
		}
	}

	pp.Println(snapshot)

	if err := r.client.Send(snapshot); err != nil {
		return fmt.Errorf("failed to send report: %w", err)
	}
	return nil
}
