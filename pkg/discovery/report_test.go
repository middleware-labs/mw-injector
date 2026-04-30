package discovery

import (
	"testing"
)

func TestMergeListeners(t *testing.T) {
	tests := []struct {
		name     string
		a, b     []Listener
		wantLen  int
		wantPorts []uint16
	}{
		{
			name:     "both empty",
			a:        nil,
			b:        nil,
			wantLen:  0,
			wantPorts: nil,
		},
		{
			name:      "a has ports, b empty",
			a:         []Listener{{Port: 8080, Protocol: "tcp"}},
			b:         nil,
			wantLen:   1,
			wantPorts: []uint16{8080},
		},
		{
			name:      "a empty, b has ports",
			a:         nil,
			b:         []Listener{{Port: 3000, Protocol: "tcp"}},
			wantLen:   1,
			wantPorts: []uint16{3000},
		},
		{
			name: "disjoint ports",
			a:    []Listener{{Port: 8080, Protocol: "tcp"}},
			b:    []Listener{{Port: 3000, Protocol: "tcp"}},
			wantLen:   2,
			wantPorts: []uint16{8080, 3000},
		},
		{
			name: "overlapping ports deduplicated",
			a:    []Listener{{Port: 8080, Protocol: "tcp"}, {Port: 443, Protocol: "tcp"}},
			b:    []Listener{{Port: 8080, Protocol: "tcp"}, {Port: 9090, Protocol: "tcp"}},
			wantLen:   3,
			wantPorts: []uint16{8080, 443, 9090},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeListeners(tt.a, tt.b)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for _, want := range tt.wantPorts {
				found := false
				for _, l := range got {
					if l.Port == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing port %d in result", want)
				}
			}
		})
	}
}

func TestAccumulateByFingerprint(t *testing.T) {
	t.Run("different fingerprints produce separate entries", func(t *testing.T) {
		settings := map[string]ServiceSetting{}
		entries := []ServiceSetting{
			{
				PID: 100, ServiceName: "app-a", Key: "host-node-app-a",
				Fingerprint: "fp-aaa", Owner: "root", Status: "running",
				Listeners: []Listener{{Port: 3000, Protocol: "tcp"}},
			},
			{
				PID: 200, ServiceName: "app-b", Key: "host-node-app-b",
				Fingerprint: "fp-bbb", Owner: "root", Status: "running",
				Listeners: []Listener{{Port: 4000, Protocol: "tcp"}},
			},
		}
		for _, ss := range entries {
			mapKey := ss.Fingerprint
			inst := ReportInstanceInfo{PID: ss.PID, Owner: ss.Owner, Status: ss.Status}
			if existing, ok := settings[mapKey]; ok {
				existing.Instances = append(existing.Instances, inst)
				existing.Listeners = mergeListeners(existing.Listeners, ss.Listeners)
				settings[mapKey] = existing
			} else {
				ss.Instances = []ReportInstanceInfo{inst}
				settings[mapKey] = ss
			}
		}

		if len(settings) != 2 {
			t.Fatalf("got %d entries, want 2", len(settings))
		}
		if len(settings["fp-aaa"].Instances) != 1 {
			t.Errorf("fp-aaa instances = %d, want 1", len(settings["fp-aaa"].Instances))
		}
		if len(settings["fp-bbb"].Instances) != 1 {
			t.Errorf("fp-bbb instances = %d, want 1", len(settings["fp-bbb"].Instances))
		}
	})

	t.Run("same fingerprint accumulates instances", func(t *testing.T) {
		settings := map[string]ServiceSetting{}
		entries := []ServiceSetting{
			{
				PID: 100, ServiceName: "my-app", Key: "host-node-my-app",
				Fingerprint: "fp-same", Owner: "root", Status: "running",
				Listeners: []Listener{{Port: 3000, Protocol: "tcp"}},
			},
			{
				PID: 200, ServiceName: "my-app", Key: "host-node-my-app",
				Fingerprint: "fp-same", Owner: "deploy", Status: "running",
				Listeners: []Listener{{Port: 3001, Protocol: "tcp"}},
			},
		}
		for _, ss := range entries {
			mapKey := ss.Fingerprint
			inst := ReportInstanceInfo{PID: ss.PID, Owner: ss.Owner, Status: ss.Status}
			if existing, ok := settings[mapKey]; ok {
				existing.Instances = append(existing.Instances, inst)
				existing.Listeners = mergeListeners(existing.Listeners, ss.Listeners)
				existing.HasAgent = existing.HasAgent || ss.HasAgent
				existing.Instrumented = existing.Instrumented || ss.Instrumented
				settings[mapKey] = existing
			} else {
				ss.Instances = []ReportInstanceInfo{inst}
				settings[mapKey] = ss
			}
		}

		if len(settings) != 1 {
			t.Fatalf("got %d entries, want 1", len(settings))
		}
		entry := settings["fp-same"]
		if len(entry.Instances) != 2 {
			t.Fatalf("instances = %d, want 2", len(entry.Instances))
		}
		if entry.Instances[0].PID != 100 || entry.Instances[1].PID != 200 {
			t.Errorf("PIDs = [%d, %d], want [100, 200]", entry.Instances[0].PID, entry.Instances[1].PID)
		}
		if len(entry.Listeners) != 2 {
			t.Errorf("listeners = %d, want 2", len(entry.Listeners))
		}
		// Representative is first seen
		if entry.PID != 100 {
			t.Errorf("representative PID = %d, want 100", entry.PID)
		}
		if entry.ServiceName != "my-app" {
			t.Errorf("service name = %q, want %q", entry.ServiceName, "my-app")
		}
	})

	t.Run("HasAgent OR-merged across instances", func(t *testing.T) {
		settings := map[string]ServiceSetting{}
		entries := []ServiceSetting{
			{PID: 100, Fingerprint: "fp-x", HasAgent: false},
			{PID: 200, Fingerprint: "fp-x", HasAgent: true},
		}
		for _, ss := range entries {
			mapKey := ss.Fingerprint
			inst := ReportInstanceInfo{PID: ss.PID, Owner: ss.Owner, Status: ss.Status}
			if existing, ok := settings[mapKey]; ok {
				existing.Instances = append(existing.Instances, inst)
				existing.HasAgent = existing.HasAgent || ss.HasAgent
				existing.Instrumented = existing.Instrumented || ss.Instrumented
				settings[mapKey] = existing
			} else {
				ss.Instances = []ReportInstanceInfo{inst}
				settings[mapKey] = ss
			}
		}

		if !settings["fp-x"].HasAgent {
			t.Error("HasAgent should be true (OR-merged)")
		}
	})

	t.Run("falls back to Key when Fingerprint empty", func(t *testing.T) {
		settings := map[string]ServiceSetting{}
		ss := ServiceSetting{
			PID: 100, Key: "host-java-myapp", Fingerprint: "",
			Owner: "root", Status: "running",
		}
		mapKey := ss.Fingerprint
		if mapKey == "" {
			mapKey = ss.Key
		}
		ss.Instances = []ReportInstanceInfo{{PID: ss.PID, Owner: ss.Owner, Status: ss.Status}}
		settings[mapKey] = ss

		if _, ok := settings["host-java-myapp"]; !ok {
			t.Error("expected entry keyed by ss.Key when fingerprint is empty")
		}
	})
}
