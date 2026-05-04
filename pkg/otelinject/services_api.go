package otelinject

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sort"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// ServiceEntry represents a discovered workload class, potentially with
// multiple running instances (replicas sharing the same fingerprint).
type ServiceEntry struct {
	Fingerprint     string
	ServiceName     string
	Language        string
	ServiceType     string // "systemd", "docker", "standalone"
	SystemdUnit     string
	Ports           []int
	Instances       []InstanceInfo
	Instrumented    bool
	InstrumentedVia string // "obi", "systemd", ""
}

// InstanceInfo holds per-PID details for an individual process instance.
type InstanceInfo struct {
	PID    int32
	Owner  string
	Status string
	Ports  []int
}

// DiscoverServicesOpts controls filtering for DiscoverServices.
type DiscoverServicesOpts struct {
	Language string // filter by language, empty = all
	Logger   *slog.Logger
}

// DiscoverServices runs the discovery pipeline and returns services grouped
// by fingerprint. Each ServiceEntry may contain multiple instances (replicas).
func DiscoverServices(opts DiscoverServicesOpts) ([]ServiceEntry, error) {
	rawReport, err := discovery.GetAgentReportValueWithLogger(opts.Logger)
	if err != nil {
		return nil, fmt.Errorf("discovery failed: %w", err)
	}

	osConfig, ok := rawReport[runtime.GOOS]
	if !ok {
		return nil, fmt.Errorf("no services found for %s", runtime.GOOS)
	}

	// Read OBI config once for instrumentation checks.
	var obiCfg *OBIConfig
	if cfg, err := ReadOBIConfig(DefaultOBIConfigPath); err == nil {
		obiCfg = cfg
	}

	// Group settings by fingerprint.
	type group struct {
		representative discovery.ServiceSetting
		instances      []InstanceInfo
		ports          map[int]struct{}
	}
	groups := make(map[string]*group)
	// Preserve insertion order for stable output.
	var order []string

	for _, setting := range osConfig.AutoInstrumentationSettings {
		if opts.Language != "" && setting.Language != opts.Language {
			continue
		}

		fp := setting.Fingerprint
		if fp == "" {
			fp = setting.Key
		}

		g, exists := groups[fp]
		if !exists {
			g = &group{
				representative: setting,
				ports:          make(map[int]struct{}),
			}
			groups[fp] = g
			order = append(order, fp)
		}

		if len(setting.Instances) > 0 {
			for _, ri := range setting.Instances {
				inst := InstanceInfo{
					PID:    ri.PID,
					Owner:  ri.Owner,
					Status: ri.Status,
				}
				for _, l := range ri.Listeners {
					inst.Ports = append(inst.Ports, int(l.Port))
					g.ports[int(l.Port)] = struct{}{}
				}
				sort.Ints(inst.Ports)
				g.instances = append(g.instances, inst)
			}
		} else {
			inst := InstanceInfo{
				PID:    setting.PID,
				Owner:  setting.Owner,
				Status: setting.Status,
			}
			for _, l := range setting.Listeners {
				inst.Ports = append(inst.Ports, int(l.Port))
				g.ports[int(l.Port)] = struct{}{}
			}
			sort.Ints(inst.Ports)
			g.instances = append(g.instances, inst)
		}
	}

	entries := make([]ServiceEntry, 0, len(groups))
	for _, fp := range order {
		g := groups[fp]
		s := g.representative

		ports := make([]int, 0, len(g.ports))
		for p := range g.ports {
			ports = append(ports, p)
		}
		sort.Ints(ports)

		entry := ServiceEntry{
			Fingerprint: fp,
			ServiceName: s.ServiceName,
			Language:    s.Language,
			ServiceType: s.ServiceType,
			SystemdUnit: s.SystemdUnit,
			Ports:       ports,
			Instances:   g.instances,
		}

		// Check OBI instrumentation.
		if obiCfg != nil && obiCfg.HasSelector(s.ServiceName) {
			entry.Instrumented = true
			entry.InstrumentedVia = "obi"
		}

		// Check systemd drop-in instrumentation.
		if s.SystemdUnit != "" && !entry.Instrumented {
			dropinPath := fmt.Sprintf("/etc/systemd/system/%s.service.d/middleware-otel.conf", s.SystemdUnit)
			if _, err := os.Stat(dropinPath); err == nil {
				entry.Instrumented = true
				entry.InstrumentedVia = "systemd"
			}
		}

		// Fall back to discovery-reported instrumentation (LD_PRELOAD detected).
		if !entry.Instrumented && s.Instrumented {
			entry.Instrumented = true
			entry.InstrumentedVia = s.AgentType
		}

		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Language != entries[j].Language {
			return entries[i].Language < entries[j].Language
		}
		return entries[i].ServiceName < entries[j].ServiceName
	})

	return entries, nil
}
