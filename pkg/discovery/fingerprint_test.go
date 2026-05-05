package discovery_test

import (
	"testing"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

func makeProcess(lang discovery.Language, details map[string]any) *discovery.Process {
	p := &discovery.Process{
		Language: lang,
		Details:  make(map[string]any),
	}
	for k, v := range details {
		p.Details[k] = v
	}
	return p
}

func TestFingerprint(t *testing.T) {
	tests := []struct {
		name      string
		procA     *discovery.Process
		procB     *discovery.Process
		wantEqual bool
	}{
		{
			name: "python collision prevention - different cwd, no -m",
			procA: makeProcess(discovery.LangPython, map[string]any{
				"entry_point":       "main:app",
				"working_directory": "/home/user/fastapi-app",
			}),
			procB: makeProcess(discovery.LangPython, map[string]any{
				"entry_point":       "unattended-upgrade-shutdown",
				"working_directory": "/usr/share/unattended-upgrades",
			}),
			wantEqual: false,
		},
		{
			name: "version-agnostic exe path - python 3.12 vs 3.13",
			procA: func() *discovery.Process {
				p := makeProcess(discovery.LangPython, map[string]any{
					"module_path":       "flask",
					"entry_point":       "flask",
					"working_directory": "/opt/myapp",
				})
				p.ExecutablePath = "/usr/bin/python3.12"
				return p
			}(),
			procB: func() *discovery.Process {
				p := makeProcess(discovery.LangPython, map[string]any{
					"module_path":       "flask",
					"entry_point":       "flask",
					"working_directory": "/opt/myapp",
				})
				p.ExecutablePath = "/usr/bin/python3.13"
				return p
			}(),
			wantEqual: true,
		},
		{
			name: "version-agnostic jar - 1.0.0 vs 1.0.1",
			procA: makeProcess(discovery.LangJava, map[string]any{
				"jar.file":          "app-1.0.0.jar",
				"main.class":       "com.example.Main",
				"working_directory": "/opt/myapp",
			}),
			procB: makeProcess(discovery.LangJava, map[string]any{
				"jar.file":          "app-1.0.1.jar",
				"main.class":       "com.example.Main",
				"working_directory": "/opt/myapp",
			}),
			wantEqual: true,
		},
		{
			name: "same workload different PIDs",
			procA: func() *discovery.Process {
				p := makeProcess(discovery.LangNode, map[string]any{
					"entry_point":       "index.js",
					"working_directory": "/opt/myapp",
				})
				p.PID = 1000
				return p
			}(),
			procB: func() *discovery.Process {
				p := makeProcess(discovery.LangNode, map[string]any{
					"entry_point":       "index.js",
					"working_directory": "/opt/myapp",
				})
				p.PID = 2000
				return p
			}(),
			wantEqual: true,
		},
		{
			name: "python with -m flag",
			procA: makeProcess(discovery.LangPython, map[string]any{
				"module_path":       "flask",
				"entry_point":       "flask",
				"working_directory": "/opt/myapp",
			}),
			procB: makeProcess(discovery.LangPython, map[string]any{
				"working_directory": "/opt/myapp",
			}),
			wantEqual: false,
		},
		{
			name: "python with uvicorn - entry point discriminates",
			procA: makeProcess(discovery.LangPython, map[string]any{
				"entry_point":       "main:app",
				"working_directory": "/opt/api",
			}),
			procB: makeProcess(discovery.LangPython, map[string]any{
				"entry_point":       "worker:celery",
				"working_directory": "/opt/api",
			}),
			wantEqual: false,
		},
		{
			name: "node with package name differentiates",
			procA: makeProcess(discovery.LangNode, map[string]any{
				"entry_point":       "index.js",
				"package_name":      "my-api",
				"working_directory": "/opt/app",
			}),
			procB: makeProcess(discovery.LangNode, map[string]any{
				"entry_point":       "index.js",
				"working_directory": "/opt/app",
			}),
			wantEqual: false,
		},
		{
			name: "systemd unit differentiation",
			procA: makeProcess(discovery.LangPython, map[string]any{
				"systemd_unit":      "api.service",
				"working_directory": "/opt/myapp",
			}),
			procB: makeProcess(discovery.LangPython, map[string]any{
				"systemd_unit":      "worker.service",
				"working_directory": "/opt/myapp",
			}),
			wantEqual: false,
		},
		{
			name: "container name differentiation",
			procA: func() *discovery.Process {
				p := makeProcess(discovery.LangJava, map[string]any{
					"jar.file":          "app.jar",
					"working_directory": "/opt/app",
				})
				p.ContainerInfo = &discovery.ContainerInfo{ContainerName: "api-blue"}
				return p
			}(),
			procB: func() *discovery.Process {
				p := makeProcess(discovery.LangJava, map[string]any{
					"jar.file":          "app.jar",
					"working_directory": "/opt/app",
				})
				p.ContainerInfo = &discovery.ContainerInfo{ContainerName: "api-green"}
				return p
			}(),
			wantEqual: false,
		},
		{
			name: "explicit service name does not affect fingerprint",
			procA: makeProcess(discovery.LangNode, map[string]any{
				"entry_point":           "index.js",
				"working_directory":     "/opt/app",
				"explicit_service_name": "billing-api",
			}),
			procB: makeProcess(discovery.LangNode, map[string]any{
				"entry_point":       "index.js",
				"working_directory": "/opt/app",
			}),
			wantEqual: true,
		},
		{
			name: "cross-language non-collision",
			procA: makeProcess(discovery.LangJava, map[string]any{
				"working_directory": "/opt/myapp",
			}),
			procB: makeProcess(discovery.LangPython, map[string]any{
				"working_directory": "/opt/myapp",
			}),
			wantEqual: false,
		},
		{
			name: "sorting stability - same details produce same hash",
			procA: func() *discovery.Process {
				p := makeProcess(discovery.LangPython, map[string]any{})
				p.Details["systemd_unit"] = "myapp.service"
				p.Details["working_directory"] = "/opt/myapp"
				p.Details["entry_point"] = "main.py"
				return p
			}(),
			procB: func() *discovery.Process {
				p := makeProcess(discovery.LangPython, map[string]any{})
				p.Details["entry_point"] = "main.py"
				p.Details["working_directory"] = "/opt/myapp"
				p.Details["systemd_unit"] = "myapp.service"
				return p
			}(),
			wantEqual: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fpA := tt.procA.Fingerprint()
			fpB := tt.procB.Fingerprint()

			if fpA == "" {
				t.Error("procA fingerprint is empty")
			}
			if fpB == "" {
				t.Error("procB fingerprint is empty")
			}

			if tt.wantEqual && fpA != fpB {
				t.Errorf("expected equal fingerprints, got %q vs %q", fpA, fpB)
			}
			if !tt.wantEqual && fpA == fpB {
				t.Errorf("expected different fingerprints, both are %q", fpA)
			}
		})
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	p := makeProcess(discovery.LangJava, map[string]any{
		"jar.file":          "myapp-2.0.0.jar",
		"main.class":       "com.example.App",
		"working_directory": "/opt/myapp",
		"systemd_unit":      "myapp.service",
	})

	fp1 := p.Fingerprint()
	fp2 := p.Fingerprint()
	if fp1 != fp2 {
		t.Errorf("fingerprint not deterministic: %q vs %q", fp1, fp2)
	}
}
