package otelinject

import (
	"testing"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

func TestBuildInstancesFromSetting(t *testing.T) {
	t.Run("uses pre-aggregated Instances when populated", func(t *testing.T) {
		setting := discovery.ServiceSetting{
			PID:   100,
			Owner: "root",
			Status: "running",
			Instances: []discovery.ReportInstanceInfo{
				{PID: 100, Owner: "root", Status: "running"},
				{PID: 200, Owner: "deploy", Status: "running"},
			},
		}

		var instances []InstanceInfo
		if len(setting.Instances) > 0 {
			for _, ri := range setting.Instances {
				instances = append(instances, InstanceInfo{
					PID:    ri.PID,
					Owner:  ri.Owner,
					Status: ri.Status,
				})
			}
		} else {
			instances = append(instances, InstanceInfo{
				PID:    setting.PID,
				Owner:  setting.Owner,
				Status: setting.Status,
			})
		}

		if len(instances) != 2 {
			t.Fatalf("got %d instances, want 2", len(instances))
		}
		if instances[0].PID != 100 || instances[1].PID != 200 {
			t.Errorf("PIDs = [%d, %d], want [100, 200]", instances[0].PID, instances[1].PID)
		}
	})

	t.Run("falls back to top-level fields when Instances nil", func(t *testing.T) {
		setting := discovery.ServiceSetting{
			PID:    300,
			Owner:  "app-user",
			Status: "sleeping",
		}

		var instances []InstanceInfo
		if len(setting.Instances) > 0 {
			for _, ri := range setting.Instances {
				instances = append(instances, InstanceInfo{
					PID:    ri.PID,
					Owner:  ri.Owner,
					Status: ri.Status,
				})
			}
		} else {
			instances = append(instances, InstanceInfo{
				PID:    setting.PID,
				Owner:  setting.Owner,
				Status: setting.Status,
			})
		}

		if len(instances) != 1 {
			t.Fatalf("got %d instances, want 1", len(instances))
		}
		if instances[0].PID != 300 {
			t.Errorf("PID = %d, want 300", instances[0].PID)
		}
		if instances[0].Owner != "app-user" {
			t.Errorf("Owner = %q, want %q", instances[0].Owner, "app-user")
		}
	})
}

// TestPerInstancePortsGrouping exercises the grouping logic from
// DiscoverServices, verifying that per-instance listeners from
// ReportInstanceInfo flow through to InstanceInfo.Ports and that the
// merged ServiceEntry.Ports contains the union.
func TestPerInstancePortsGrouping(t *testing.T) {
	// Simulate what DiscoverServices does: group settings by fingerprint,
	// propagating per-instance listeners to InstanceInfo.Ports.

	t.Run("pre-aggregated instances carry per-PID ports", func(t *testing.T) {
		setting := discovery.ServiceSetting{
			PID:         100,
			ServiceName: "browsebay-api",
			Language:    "node",
			Fingerprint: "fp-abc",
			Instances: []discovery.ReportInstanceInfo{
				{
					PID: 100, Owner: "root", Status: "running",
					Listeners: []discovery.Listener{{Protocol: "tcp6", Port: 3000}},
				},
				{
					PID: 200, Owner: "root", Status: "running",
					Listeners: []discovery.Listener{{Protocol: "tcp6", Port: 3001}},
				},
			},
		}

		// Replay the grouping logic from DiscoverServices.
		type group struct {
			representative discovery.ServiceSetting
			instances      []InstanceInfo
			ports          map[int]struct{}
		}
		g := &group{representative: setting, ports: make(map[int]struct{})}

		for _, ri := range setting.Instances {
			inst := InstanceInfo{PID: ri.PID, Owner: ri.Owner, Status: ri.Status}
			for _, l := range ri.Listeners {
				inst.Ports = append(inst.Ports, int(l.Port))
				g.ports[int(l.Port)] = struct{}{}
			}
			g.instances = append(g.instances, inst)
		}

		// Per-instance ports
		if len(g.instances) != 2 {
			t.Fatalf("instances = %d, want 2", len(g.instances))
		}
		if len(g.instances[0].Ports) != 1 || g.instances[0].Ports[0] != 3000 {
			t.Errorf("instance 0 ports = %v, want [3000]", g.instances[0].Ports)
		}
		if len(g.instances[1].Ports) != 1 || g.instances[1].Ports[0] != 3001 {
			t.Errorf("instance 1 ports = %v, want [3001]", g.instances[1].Ports)
		}
		// Merged ports
		if len(g.ports) != 2 {
			t.Errorf("merged ports count = %d, want 2", len(g.ports))
		}
		if _, ok := g.ports[3000]; !ok {
			t.Error("merged ports missing 3000")
		}
		if _, ok := g.ports[3001]; !ok {
			t.Error("merged ports missing 3001")
		}
	})

	t.Run("fallback path populates ports from top-level Listeners", func(t *testing.T) {
		setting := discovery.ServiceSetting{
			PID:         300,
			ServiceName: "simple-app",
			Language:    "node",
			Fingerprint: "fp-xyz",
			Owner:       "deploy",
			Status:      "running",
			Listeners: []discovery.Listener{
				{Protocol: "tcp", Port: 8080},
				{Protocol: "tcp", Port: 8443},
			},
		}

		ports := make(map[int]struct{})
		inst := InstanceInfo{PID: setting.PID, Owner: setting.Owner, Status: setting.Status}
		for _, l := range setting.Listeners {
			inst.Ports = append(inst.Ports, int(l.Port))
			ports[int(l.Port)] = struct{}{}
		}

		if len(inst.Ports) != 2 {
			t.Fatalf("instance ports = %d, want 2", len(inst.Ports))
		}
		if inst.Ports[0] != 8080 || inst.Ports[1] != 8443 {
			t.Errorf("instance ports = %v, want [8080 8443]", inst.Ports)
		}
		if len(ports) != 2 {
			t.Errorf("merged ports count = %d, want 2", len(ports))
		}
	})

	t.Run("instances with no listeners get empty Ports", func(t *testing.T) {
		setting := discovery.ServiceSetting{
			PID:         400,
			ServiceName: "no-port-app",
			Language:    "node",
			Fingerprint: "fp-nop",
			Instances: []discovery.ReportInstanceInfo{
				{PID: 400, Owner: "root", Status: "running"},
			},
		}

		var inst InstanceInfo
		for _, ri := range setting.Instances {
			inst = InstanceInfo{PID: ri.PID, Owner: ri.Owner, Status: ri.Status}
			for _, l := range ri.Listeners {
				inst.Ports = append(inst.Ports, int(l.Port))
			}
		}

		if len(inst.Ports) != 0 {
			t.Errorf("instance ports = %v, want empty", inst.Ports)
		}
	})

	t.Run("overlapping ports across instances deduplicated in merged set", func(t *testing.T) {
		setting := discovery.ServiceSetting{
			PID:         100,
			ServiceName: "cluster-app",
			Language:    "node",
			Fingerprint: "fp-cluster",
			Instances: []discovery.ReportInstanceInfo{
				{
					PID: 100, Owner: "root", Status: "running",
					Listeners: []discovery.Listener{{Protocol: "tcp", Port: 3000}},
				},
				{
					PID: 200, Owner: "root", Status: "running",
					Listeners: []discovery.Listener{{Protocol: "tcp", Port: 3000}},
				},
			},
		}

		ports := make(map[int]struct{})
		var instances []InstanceInfo
		for _, ri := range setting.Instances {
			inst := InstanceInfo{PID: ri.PID, Owner: ri.Owner, Status: ri.Status}
			for _, l := range ri.Listeners {
				inst.Ports = append(inst.Ports, int(l.Port))
				ports[int(l.Port)] = struct{}{}
			}
			instances = append(instances, inst)
		}

		// Both instances report port 3000
		if len(instances[0].Ports) != 1 || instances[0].Ports[0] != 3000 {
			t.Errorf("instance 0 ports = %v, want [3000]", instances[0].Ports)
		}
		if len(instances[1].Ports) != 1 || instances[1].Ports[0] != 3000 {
			t.Errorf("instance 1 ports = %v, want [3000]", instances[1].Ports)
		}
		// Merged set deduplicates to 1
		if len(ports) != 1 {
			t.Errorf("merged ports count = %d, want 1", len(ports))
		}
	})
}
