package otelinject

import (
	"fmt"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// InstrumentationStrategy defines a method of applying OTel instrumentation
// to discovered processes. Different strategies (systemd dropin, OBI, Docker
// env injection) implement this interface and are selected based on the
// process's deployment context.
//
// Adding a new instrumentation method means implementing this interface and
// registering it in a StrategyRegistry — no changes to discovery or the
// individual language handlers are required.
type InstrumentationStrategy interface {
	// Name returns a human-readable identifier for this strategy
	// (e.g. "systemd-dropin", "obi", "docker-env").
	Name() string

	// CanHandle returns true if this strategy can instrument the given service.
	// For example, the systemd strategy returns true only for services
	// managed by a systemd unit.
	CanHandle(service discovery.ServiceSetting) bool

	// ValidateAssets checks that required agent files are present for the
	// given language. Returns an error describing what's missing.
	ValidateAssets(lang discovery.Language, baseDir string) error

	// Instrument applies instrumentation to a single service. The language
	// is used to select language-specific environment variables or dropin
	// content (e.g. Python needs PYTHONPATH, Java/Node use LD_PRELOAD only).
	Instrument(service discovery.ServiceSetting, lang discovery.Language) error

	// Uninstrument removes instrumentation from a single service.
	Uninstrument(service discovery.ServiceSetting) error
}

// StrategyRegistry holds registered InstrumentationStrategies and selects
// the appropriate one for a given service. Strategies are tried in
// registration order — the first one whose CanHandle returns true wins.
type StrategyRegistry struct {
	strategies []InstrumentationStrategy
}

// NewStrategyRegistry returns a registry pre-loaded with the default
// strategies. Currently only the systemd dropin strategy is registered.
func NewStrategyRegistry() *StrategyRegistry {
	return &StrategyRegistry{
		strategies: []InstrumentationStrategy{
			&SystemdDropinStrategy{},
			NewOBIStrategy(),
		},
	}
}

// Register adds a strategy to the registry. Strategies added later are
// tried after earlier ones.
func (r *StrategyRegistry) Register(s InstrumentationStrategy) {
	r.strategies = append(r.strategies, s)
}

// ForService returns the first strategy that can handle the given service,
// or nil if none can.
func (r *StrategyRegistry) ForService(service discovery.ServiceSetting) InstrumentationStrategy {
	for _, s := range r.strategies {
		if s.CanHandle(service) {
			return s
		}
	}
	return nil
}

// InstrumentService finds the right strategy for the service and applies
// instrumentation. Returns an error if no strategy can handle the service.
func (r *StrategyRegistry) InstrumentService(service discovery.ServiceSetting, lang discovery.Language) error {
	strategy := r.ForService(service)
	if strategy == nil {
		return fmt.Errorf("no instrumentation strategy available for service %q (type=%s)", service.ServiceName, service.ServiceType)
	}

	if err := strategy.ValidateAssets(lang, ""); err != nil {
		return fmt.Errorf("asset validation failed for %s strategy: %w", strategy.Name(), err)
	}

	return strategy.Instrument(service, lang)
}

// UninstrumentService finds the right strategy and removes instrumentation.
func (r *StrategyRegistry) UninstrumentService(service discovery.ServiceSetting) error {
	strategy := r.ForService(service)
	if strategy == nil {
		return fmt.Errorf("no instrumentation strategy available for service %q", service.ServiceName)
	}

	return strategy.Uninstrument(service)
}
