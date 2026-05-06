package otelinject

import (
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"time"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// InstrumentOBI instruments a service via OBI by adding an OBI selector to the
// OBI config and restarting the obi-agent. The identifier can be a service
// name or a fingerprint.
func InstrumentOBI(identifier string, lang Language, logger *slog.Logger) error {
	allSettings, err := discoverServiceSettings(lang, logger)
	if err != nil {
		return err
	}

	setting, err := findServiceFrom(identifier, allSettings)
	if err != nil {
		return fmt.Errorf("service %q (language: %s) not found in %d discovered services: %w", identifier, lang, len(allSettings), err)
	}

	setting.Listeners = collectPortsForFingerprint(setting.Fingerprint, allSettings)
	if len(setting.Listeners) == 0 {
		pids := collectPIDsForFingerprint(setting.Fingerprint, allSettings)
		setting.Listeners = awaitListeners(pids, logger)
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

	type group struct {
		representative discovery.ServiceSetting
	}
	groups := make(map[string]*group)
	var order []string
	for _, s := range settings {
		fp := s.Fingerprint
		if _, ok := groups[fp]; !ok {
			groups[fp] = &group{representative: s}
			order = append(order, fp)
		}
	}

	for _, fp := range order {
		g := groups[fp]
		g.representative.Listeners = collectPortsForFingerprint(fp, settings)
		if len(g.representative.Listeners) == 0 {
			pids := collectPIDsForFingerprint(fp, settings)
			g.representative.Listeners = awaitListeners(pids, logger)
		}

		sel := buildOBISelector(g.representative, lang)
		overwritten, err := cfg.AddSelector(sel)
		if err != nil {
			return fmt.Errorf("add selector for %s: %w", g.representative.ServiceName, err)
		}
		if overwritten && logger != nil {
			logger.Info("overwriting existing OBI selector", "name", sel.Name)
		}
	}

	if err := cfg.Write(strategy.configPath); err != nil {
		return fmt.Errorf("write OBI config: %w", err)
	}

	return strategy.restartOBI()
}

// findService discovers services and returns the ServiceSetting matching the
// given identifier (tried as service name first, then fingerprint) and language.
func findService(identifier string, lang Language, logger *slog.Logger) (discovery.ServiceSetting, error) {
	settings, err := discoverServiceSettings(lang, logger)
	if err != nil {
		return discovery.ServiceSetting{}, err
	}
	return findServiceFrom(identifier, settings)
}

// findServiceFrom returns the ServiceSetting matching the given identifier
// from a pre-fetched settings slice. Tries name match first, then fingerprint.
func findServiceFrom(identifier string, settings []discovery.ServiceSetting) (discovery.ServiceSetting, error) {
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

	return discovery.ServiceSetting{}, fmt.Errorf("no service matching %q found", identifier)
}

// collectPortsForFingerprint returns deduplicated, sorted listeners from all
// settings that share the given fingerprint.
func collectPortsForFingerprint(fp string, all []discovery.ServiceSetting) []discovery.Listener {
	seen := make(map[uint16]struct{})
	var listeners []discovery.Listener
	for _, s := range all {
		if s.Fingerprint != fp {
			continue
		}
		for _, l := range s.Listeners {
			if _, ok := seen[l.Port]; !ok {
				seen[l.Port] = struct{}{}
				listeners = append(listeners, l)
			}
		}
	}
	sort.Slice(listeners, func(i, j int) bool {
		return listeners[i].Port < listeners[j].Port
	})
	return listeners
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

// collectPIDsForFingerprint returns all unique PIDs from settings that share
// the given fingerprint.
func collectPIDsForFingerprint(fp string, all []discovery.ServiceSetting) []int32 {
	seen := make(map[int32]struct{})
	var pids []int32
	for _, s := range all {
		if s.Fingerprint != fp || s.PID <= 0 {
			continue
		}
		if _, ok := seen[s.PID]; !ok {
			seen[s.PID] = struct{}{}
			pids = append(pids, s.PID)
		}
	}
	return pids
}

// awaitListeners polls /proc/<pid>/net/* for listening sockets across the
// given PIDs. Used when initial discovery found no ports — typically because
// a process was just restarted and hasn't bound its socket yet.
func awaitListeners(pids []int32, logger *slog.Logger) []discovery.Listener {
	if len(pids) == 0 {
		return nil
	}

	const (
		interval = 500 * time.Millisecond
		timeout  = 15 * time.Second
	)

	if logger != nil {
		logger.Info("no ports detected, waiting for process to bind", "pids", pids, "timeout", timeout)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(interval)

		seen := make(map[uint16]struct{})
		var listeners []discovery.Listener
		for _, pid := range pids {
			for _, l := range discovery.ListListeners(pid) {
				if _, ok := seen[l.Port]; !ok {
					seen[l.Port] = struct{}{}
					listeners = append(listeners, l)
				}
			}
		}
		if len(listeners) > 0 {
			sort.Slice(listeners, func(i, j int) bool {
				return listeners[i].Port < listeners[j].Port
			})
			if logger != nil {
				logger.Info("ports detected after waiting", "listeners", listeners)
			}
			return listeners
		}
	}

	if logger != nil {
		logger.Warn("timed out waiting for ports to bind", "pids", pids)
	}
	return nil
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
