package otelinject

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

const (
	defaultOBIBinary     = "/usr/local/bin/obi"
	defaultOBIUnitName   = "obi-agent"
	obiRestartTimeout    = 10 * time.Second
	obiRestartPollPeriod = 500 * time.Millisecond
)

// OBIStrategy instruments services by adding OBI selectors to the OBI agent's
// YAML config and restarting the obi-agent systemd service. Unlike the systemd
// drop-in strategy, OBI can instrument any process — including non-systemd ones.
type OBIStrategy struct {
	configPath string
	unitName   string
	logger     *slog.Logger
}

func NewOBIStrategy() *OBIStrategy {
	return &OBIStrategy{
		configPath: DefaultOBIConfigPath,
		unitName:   defaultOBIUnitName,
	}
}

func NewOBIStrategyWithLogger(logger *slog.Logger) *OBIStrategy {
	s := NewOBIStrategy()
	s.logger = logger
	return s
}

func (s *OBIStrategy) Name() string { return "obi" }

// CanHandle returns true when:
// 1. The service explicitly requests OBI instrumentation (InstrumentationType == "obi")
// 2. OBI is actually installed and its systemd service exists
func (s *OBIStrategy) CanHandle(service discovery.ServiceSetting) bool {
	if service.InstrumentationType != "obi" {
		return false
	}
	if _, err := os.Stat(defaultOBIBinary); err != nil {
		return false
	}
	return systemctlUnitExists(s.unitName)
}

// ValidateAssets checks that OBI infrastructure is in place: binary, config
// file, and active systemd unit.
func (s *OBIStrategy) ValidateAssets(_ discovery.Language, _ string) error {
	if _, err := os.Stat(defaultOBIBinary); err != nil {
		return fmt.Errorf("obi binary not found at %s", defaultOBIBinary)
	}
	if _, err := os.Stat(s.configPath); err != nil {
		return fmt.Errorf("obi config not found at %s", s.configPath)
	}
	if !systemctlIsActive(s.unitName) {
		return fmt.Errorf("obi-agent systemd service is not active")
	}
	return nil
}

// Instrument builds an OBI selector from the ServiceSetting, adds it to
// the OBI config, and restarts the obi-agent.
func (s *OBIStrategy) Instrument(service discovery.ServiceSetting, lang discovery.Language) error {
	if len(service.Listeners) == 0 {
		s.log("WARNING: no listening ports detected for service, OBI selector will match by language only",
			"service", service.ServiceName)
	}
	sel := buildOBISelector(service, lang)

	cfg, err := ReadOBIConfig(s.configPath)
	if err != nil {
		return fmt.Errorf("read obi config: %w", err)
	}

	overwritten, err := cfg.AddSelector(sel)
	if err != nil {
		return fmt.Errorf("add selector: %w", err)
	}
	if overwritten {
		s.log("overwriting existing OBI selector", "name", sel.Name)
	}

	if err := cfg.Write(s.configPath); err != nil {
		return fmt.Errorf("write obi config: %w", err)
	}

	return s.restartOBI()
}

// Uninstrument removes the selector from OBI config and restarts the agent.
func (s *OBIStrategy) Uninstrument(service discovery.ServiceSetting) error {
	cfg, err := ReadOBIConfig(s.configPath)
	if err != nil {
		return fmt.Errorf("read obi config: %w", err)
	}

	if !cfg.RemoveSelector(service.ServiceName) {
		return fmt.Errorf("no OBI selector found for service %q", service.ServiceName)
	}

	if err := cfg.Write(s.configPath); err != nil {
		return fmt.Errorf("write obi config: %w", err)
	}

	return s.restartOBI()
}

var obiLanguageMap = map[discovery.Language]string{
	discovery.LangJava:   "java",
	discovery.LangNode:   "nodejs",
	discovery.LangPython: "python",
	discovery.LangGo:     "go",
}

func buildOBISelector(service discovery.ServiceSetting, lang discovery.Language) OBISelector {
	obiLang := string(lang)
	if mapped, ok := obiLanguageMap[lang]; ok {
		obiLang = mapped
	}

	sel := OBISelector{
		Name:      service.ServiceName,
		Languages: obiLang,
	}

	if service.ServiceType != "standalone" && service.ServiceType != "systemd" && service.ServiceType != "system" {
		sel.ContainersOnly = true
	}

	// Build port list from listeners
	if len(service.Listeners) > 0 {
		var b strings.Builder
		for i, l := range service.Listeners {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%d", l.Port)
		}
		sel.OpenPorts = b.String()
	}

	// Add cmd_args for disambiguation when available
	switch lang {
	case discovery.LangJava:
		if service.JarFile != "" {
			sel.CmdArgs = "*" + service.JarFile + "*"
		} else if service.MainClass != "" {
			sel.CmdArgs = "*" + service.MainClass + "*"
		}
	}

	return sel
}

func (s *OBIStrategy) restartOBI() error {
	cmd := exec.Command("systemctl", "restart", s.unitName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restart %s: %w\n%s", s.unitName, err, string(out))
	}

	ctx, cancel := context.WithTimeout(context.Background(), obiRestartTimeout)
	defer cancel()

	for {
		if systemctlIsActive(s.unitName) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not become active within %v", s.unitName, obiRestartTimeout)
		case <-time.After(obiRestartPollPeriod):
		}
	}
}

func (s *OBIStrategy) log(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Info(msg, args...)
	}
}

// --- systemctl helpers ---

func systemctlUnitExists(unit string) bool {
	cmd := exec.Command("systemctl", "cat", unit)
	return cmd.Run() == nil
}

func systemctlIsActive(unit string) bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", unit)
	return cmd.Run() == nil
}
