package otelinject

import (
	"fmt"
	"log/slog"
	"runtime"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// InstrumentOBI instruments a service via OBI by adding an OBI selector to the
// OBI config and restarting the obi-agent. The identifier can be a service
// name or a fingerprint.
func InstrumentOBI(identifier string, lang Language, logger *slog.Logger) error {
	setting, err := findService(identifier, lang, logger)
	if err != nil {
		return err
	}

	setting.InstrumentationType = "obi"
	strategy := NewOBIStrategyWithLogger(logger)

	if err := strategy.ValidateAssets(lang, ""); err != nil {
		return fmt.Errorf("OBI validation failed: %w", err)
	}

	return strategy.Instrument(setting, lang)
}

// UninstrumentOBI removes an OBI selector for the named service and restarts
// the obi-agent. The identifier can be a service name or a fingerprint.
func UninstrumentOBI(identifier string, logger *slog.Logger) error {
	strategy := NewOBIStrategyWithLogger(logger)

	// Try direct selector name match first.
	cfg, err := ReadOBIConfig(strategy.configPath)
	if err == nil && cfg.HasSelector(identifier) {
		return strategy.Uninstrument(discovery.ServiceSetting{ServiceName: identifier})
	}

	// Identifier might be a fingerprint — run discovery to resolve it.
	serviceName, err := resolveFingerprint(identifier, logger)
	if err != nil {
		return fmt.Errorf("cannot resolve %q to a service: %w", identifier, err)
	}
	return strategy.Uninstrument(discovery.ServiceSetting{ServiceName: serviceName})
}

// InstrumentOBIBulk instruments all discovered services of the given language
// via OBI.
func InstrumentOBIBulk(lang Language, logger *slog.Logger) error {
	settings, err := discoverServiceSettings(lang, logger)
	if err != nil {
		return err
	}

	if len(settings) == 0 {
		return fmt.Errorf("no %s services found", lang)
	}

	strategy := NewOBIStrategyWithLogger(logger)

	if err := strategy.ValidateAssets(lang, ""); err != nil {
		return fmt.Errorf("OBI validation failed: %w", err)
	}

	cfg, err := ReadOBIConfig(strategy.configPath)
	if err != nil {
		return fmt.Errorf("read OBI config: %w", err)
	}

	for _, setting := range settings {
		sel := buildOBISelector(setting, lang)
		overwritten, err := cfg.AddSelector(sel)
		if err != nil {
			return fmt.Errorf("add selector for %s: %w", setting.ServiceName, err)
		}
		if overwritten && logger != nil {
			logger.Info("overwriting existing OBI selector", "name", sel.Name)
		}
	}

	if err := cfg.Write(strategy.configPath); err != nil {
		return fmt.Errorf("write OBI config: %w", err)
	}

	if err := strategy.restartOBI(); err != nil {
		return err
	}

	return nil
}

// findService discovers services and returns the ServiceSetting matching the
// given identifier (tried as service name first, then fingerprint) and language.
func findService(identifier string, lang Language, logger *slog.Logger) (discovery.ServiceSetting, error) {
	settings, err := discoverServiceSettings(lang, logger)
	if err != nil {
		return discovery.ServiceSetting{}, err
	}

	// Try exact name match.
	var nameMatches []discovery.ServiceSetting
	for _, s := range settings {
		if s.ServiceName == identifier {
			nameMatches = append(nameMatches, s)
		}
	}

	if len(nameMatches) == 1 {
		return nameMatches[0], nil
	}

	if len(nameMatches) > 1 {
		fp := nameMatches[0].Fingerprint
		allSame := true
		for _, m := range nameMatches[1:] {
			if m.Fingerprint != fp {
				allSame = false
				break
			}
		}
		if allSame {
			return nameMatches[0], nil
		}
		return discovery.ServiceSetting{}, fmt.Errorf(
			"multiple different workloads named %q found (fingerprints: %s); use fingerprint to disambiguate",
			identifier, collectFingerprints(nameMatches))
	}

	// Try fingerprint match.
	for _, s := range settings {
		if s.Fingerprint == identifier {
			return s, nil
		}
	}

	return discovery.ServiceSetting{}, fmt.Errorf(
		"service %q (language: %s) not found in discovered services", identifier, lang)
}

// resolveFingerprint runs discovery across all languages and returns the
// service name for the given fingerprint.
func resolveFingerprint(fingerprint string, logger *slog.Logger) (string, error) {
	rawReport, err := discovery.GetAgentReportValueWithLogger(logger)
	if err != nil {
		return "", fmt.Errorf("discovery failed: %w", err)
	}

	osConfig, ok := rawReport[runtime.GOOS]
	if !ok {
		return "", fmt.Errorf("no services found for %s", runtime.GOOS)
	}

	for _, s := range osConfig.AutoInstrumentationSettings {
		if s.Fingerprint == fingerprint {
			return s.ServiceName, nil
		}
	}
	return "", fmt.Errorf("no service with fingerprint %q found", fingerprint)
}

func collectFingerprints(settings []discovery.ServiceSetting) string {
	seen := make(map[string]struct{})
	var fps []string
	for _, s := range settings {
		if _, ok := seen[s.Fingerprint]; !ok {
			seen[s.Fingerprint] = struct{}{}
			fps = append(fps, s.Fingerprint)
		}
	}
	return fmt.Sprintf("%v", fps)
}

// discoverServiceSettings runs the discovery pipeline and converts the results
// to ServiceSettings via each handler's ToServiceSetting.
func discoverServiceSettings(lang Language, logger *slog.Logger) ([]discovery.ServiceSetting, error) {
	rawReport, err := discovery.GetAgentReportValueWithLogger(logger)
	if err != nil {
		return nil, fmt.Errorf("discovery failed: %w", err)
	}

	osConfig, ok := rawReport[runtime.GOOS]
	if !ok {
		return nil, fmt.Errorf("no services found for %s", runtime.GOOS)
	}

	var result []discovery.ServiceSetting
	for _, setting := range osConfig.AutoInstrumentationSettings {
		if discovery.Language(setting.Language) == lang {
			result = append(result, setting)
		}
	}
	return result, nil
}

// ListOBIServices returns discovered services suitable for OBI instrumentation
// (both systemd and non-systemd).
func ListOBIServices(logger *slog.Logger) ([]ServiceInfo, error) {
	rawReport, err := discovery.GetAgentReportValueWithLogger(logger)

	osConfig, ok := rawReport[runtime.GOOS]
	if !ok {
		return nil, fmt.Errorf("no services found for %s", runtime.GOOS)
	}

	// Check if OBI is available
	strategy := NewOBIStrategy()
	obiAvailable := strategy.CanHandle(discovery.ServiceSetting{InstrumentationType: "obi"})

	var services []ServiceInfo
	for _, setting := range osConfig.AutoInstrumentationSettings {
		info := ServiceInfo{
			Name:         setting.ServiceName,
			Language:     setting.Language,
			PID:          setting.PID,
			Owner:        setting.Owner,
			Status:       setting.Status,
			Instrumented: setting.Instrumented,
			SystemdUnit:  setting.SystemdUnit,
			AgentType:    setting.AgentType,
		}
		if obiAvailable {
			info.Instrumented = info.Instrumented || checkOBIInstrumented(setting.ServiceName, strategy.configPath)
		}
		services = append(services, info)
	}

	return services, err
}

func checkOBIInstrumented(serviceName string, configPath string) bool {
	cfg, err := ReadOBIConfig(configPath)
	if err != nil {
		return false
	}
	return cfg.HasSelector(serviceName)
}
