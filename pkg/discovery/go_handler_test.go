package discovery

import (
	"os"
	"testing"
)

func TestGoHandler_Detect(t *testing.T) {
	h := &GoHandler{}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	// The test binary itself is a Go binary — should detect.
	proc := &ProcessInfo{
		PID:     1,
		ExeName: "discovery.test",
		ExePath: self,
		CmdLine: self,
	}
	if !h.Detect(proc) {
		t.Errorf("Detect should return true for Go test binary")
	}

	// A non-Go binary (e.g. /bin/sh) should not detect.
	proc = &ProcessInfo{
		PID:     2,
		ExeName: "sh",
		ExePath: "/bin/sh",
		CmdLine: "/bin/sh",
	}
	if h.Detect(proc) {
		t.Errorf("Detect should return false for /bin/sh")
	}
}

func TestGoHandler_Detect_IgnoredBinaries(t *testing.T) {
	h := &GoHandler{}

	for _, name := range []string{"dockerd", "kubelet", "mw-agent", "obi-agent", "gopls"} {
		proc := &ProcessInfo{
			PID:     1,
			ExeName: name,
			ExePath: "/usr/bin/" + name,
			CmdLine: "/usr/bin/" + name,
		}
		if h.Detect(proc) {
			t.Errorf("Detect should return false for ignored binary %q", name)
		}
	}
}

func TestIsIgnoredGoBinary(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"dockerd", true},
		{"docker-proxy", true},
		{"kubelet", true},
		{"mw-agent", true},
		{"mw-host-agent", true},
		{"obi", true},
		{"containerd-shim-runc-v2", true},
		{"containerd-shim-runc-v1", true},
		{"kube-controller-manager", true},
		{"vanta", true},
		{"vanta-agent", true},
		{"gopls", true},
		{"gopls_0.21.1_go_1.26.2", true},
		{"lazydocker", true},
		{"myapp", false},
		{"api-server", false},
		{"billing-service", false},
		{"demo-golang-app", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isIgnoredGoBinary(tt.name)
			if got != tt.want {
				t.Errorf("isIgnoredGoBinary(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGoHandler_ExtractServiceName(t *testing.T) {
	h := &GoHandler{}

	tests := []struct {
		name    string
		proc    *Process
		want    string
	}{
		{
			name: "exe name used as service name",
			proc: &Process{
				PID:            1,
				ExecutableName: "billing-api",
				ExecutablePath: "/opt/billing-api",
				Language:       LangGo,
				Details:        map[string]any{},
			},
			want: "billing-api",
		},
		{
			name: "module path fallback",
			proc: &Process{
				PID:            2,
				ExecutableName: "main",
				ExecutablePath: "/tmp/main",
				Language:       LangGo,
				Details: map[string]any{
					DetailGoModule: "github.com/acme/order-service",
				},
			},
			want: "order-service",
		},
		{
			name: "generic exe falls through to module",
			proc: &Process{
				PID:            3,
				ExecutableName: "server",
				ExecutablePath: "/tmp/server",
				Language:       LangGo,
				Details: map[string]any{
					DetailGoModule: "github.com/acme/payment-gateway",
				},
			},
			want: "payment-gateway",
		},
		{
			name: "fallback to go-service",
			proc: &Process{
				PID:            4,
				ExecutableName: "app",
				ExecutablePath: "/tmp/app",
				Language:       LangGo,
				Details:        map[string]any{},
			},
			want: "go-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h.extractServiceName(tt.proc)
			if tt.proc.ServiceName != tt.want {
				t.Errorf("extractServiceName → %q, want %q", tt.proc.ServiceName, tt.want)
			}
		})
	}
}

func TestGoFingerprint(t *testing.T) {
	p1 := &Process{
		Language:       LangGo,
		ExecutablePath: "/opt/bin/myservice",
		Details: map[string]any{
			DetailGoModule:         "github.com/acme/myservice",
			DetailWorkingDirectory: "/opt/myservice",
		},
	}

	p2 := &Process{
		Language:       LangGo,
		ExecutablePath: "/opt/bin/myservice",
		Details: map[string]any{
			DetailGoModule:         "github.com/acme/myservice",
			DetailWorkingDirectory: "/opt/myservice",
		},
	}

	if p1.Fingerprint() != p2.Fingerprint() {
		t.Errorf("identical Go processes should produce same fingerprint")
	}

	p3 := &Process{
		Language:       LangGo,
		ExecutablePath: "/opt/bin/other-service",
		Details: map[string]any{
			DetailGoModule:         "github.com/acme/other-service",
			DetailWorkingDirectory: "/opt/other",
		},
	}

	if p1.Fingerprint() == p3.Fingerprint() {
		t.Errorf("different Go processes should produce different fingerprints")
	}
}
