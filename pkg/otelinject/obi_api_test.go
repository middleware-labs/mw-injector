package otelinject

import (
	"testing"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

func TestCollectPortsForFingerprint(t *testing.T) {
	tests := []struct {
		name      string
		fp        string
		settings  []discovery.ServiceSetting
		wantPorts []uint16
	}{
		{
			name: "aggregates ports from multiple instances",
			fp:   "abc123",
			settings: []discovery.ServiceSetting{
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 8080, Protocol: "tcp"}}},
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 8081, Protocol: "tcp"}}},
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 8082, Protocol: "tcp"}}},
			},
			wantPorts: []uint16{8080, 8081, 8082},
		},
		{
			name: "deduplicates same port across instances",
			fp:   "abc123",
			settings: []discovery.ServiceSetting{
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 8080, Protocol: "tcp"}}},
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 8080, Protocol: "tcp"}}},
			},
			wantPorts: []uint16{8080},
		},
		{
			name: "ignores other fingerprints",
			fp:   "abc123",
			settings: []discovery.ServiceSetting{
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 8080, Protocol: "tcp"}}},
				{Fingerprint: "def456", Listeners: []discovery.Listener{{Port: 9090, Protocol: "tcp"}}},
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 8081, Protocol: "tcp"}}},
			},
			wantPorts: []uint16{8080, 8081},
		},
		{
			name:      "no matching fingerprint returns empty",
			fp:        "missing",
			settings:  []discovery.ServiceSetting{{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 8080}}}},
			wantPorts: nil,
		},
		{
			name: "single instance unchanged",
			fp:   "abc123",
			settings: []discovery.ServiceSetting{
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 3000, Protocol: "tcp"}}},
			},
			wantPorts: []uint16{3000},
		},
		{
			name: "instance with no ports",
			fp:   "abc123",
			settings: []discovery.ServiceSetting{
				{Fingerprint: "abc123", Listeners: nil},
			},
			wantPorts: nil,
		},
		{
			name: "ports are sorted",
			fp:   "abc123",
			settings: []discovery.ServiceSetting{
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 9090, Protocol: "tcp"}}},
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 3000, Protocol: "tcp"}}},
				{Fingerprint: "abc123", Listeners: []discovery.Listener{{Port: 8080, Protocol: "tcp"}}},
			},
			wantPorts: []uint16{3000, 8080, 9090},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectPortsForFingerprint(tt.fp, tt.settings)
			if len(got) != len(tt.wantPorts) {
				t.Fatalf("got %d listeners, want %d", len(got), len(tt.wantPorts))
			}
			for i, l := range got {
				if l.Port != tt.wantPorts[i] {
					t.Errorf("listener[%d].Port = %d, want %d", i, l.Port, tt.wantPorts[i])
				}
			}
		})
	}
}

func TestFindServiceFrom(t *testing.T) {
	settings := []discovery.ServiceSetting{
		{ServiceName: "myapp", Fingerprint: "fp1", PID: 100},
		{ServiceName: "myapp", Fingerprint: "fp1", PID: 101},
		{ServiceName: "other", Fingerprint: "fp2", PID: 200},
	}

	t.Run("name match with same fingerprint returns first", func(t *testing.T) {
		got, err := findServiceFrom("myapp", settings)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.PID != 100 {
			t.Errorf("got PID %d, want 100", got.PID)
		}
	})

	t.Run("fingerprint match", func(t *testing.T) {
		got, err := findServiceFrom("fp2", settings)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ServiceName != "other" {
			t.Errorf("got name %q, want %q", got.ServiceName, "other")
		}
	})

	t.Run("ambiguous names with different fingerprints", func(t *testing.T) {
		ambiguous := []discovery.ServiceSetting{
			{ServiceName: "dup", Fingerprint: "fp1"},
			{ServiceName: "dup", Fingerprint: "fp2"},
		}
		_, err := findServiceFrom("dup", ambiguous)
		if err == nil {
			t.Fatal("expected error for ambiguous names")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := findServiceFrom("nonexistent", settings)
		if err == nil {
			t.Fatal("expected error for nonexistent service")
		}
	})
}

func TestBuildOBISelectorWithAggregatedPorts(t *testing.T) {
	setting := discovery.ServiceSetting{
		ServiceName: "myapp",
		ServiceType: "standalone",
		Listeners: []discovery.Listener{
			{Port: 8080, Protocol: "tcp"},
			{Port: 8081, Protocol: "tcp"},
			{Port: 8082, Protocol: "tcp"},
		},
	}

	sel := buildOBISelector(setting, discovery.LangJava)

	if sel.OpenPorts != "8080,8081,8082" {
		t.Errorf("got open_ports %q, want %q", sel.OpenPorts, "8080,8081,8082")
	}
}
