package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractNodeInfoPackageJson(t *testing.T) {
	tests := []struct {
		name            string
		packageJson     string
		wantPackageName string
		wantVersion     string
	}{
		{
			name:            "valid package.json with name and version",
			packageJson:     `{"name": "my-api", "version": "1.2.3"}`,
			wantPackageName: "my-api",
			wantVersion:     "1.2.3",
		},
		{
			name:            "package.json with only name",
			packageJson:     `{"name": "billing-service"}`,
			wantPackageName: "billing-service",
			wantVersion:     "",
		},
		{
			name:            "package.json with empty name",
			packageJson:     `{"name": "", "version": "1.0.0"}`,
			wantPackageName: "",
			wantVersion:     "1.0.0",
		},
		{
			name:            "scoped package name",
			packageJson:     `{"name": "@myorg/api-service", "version": "2.0.0"}`,
			wantPackageName: "@myorg/api-service",
			wantVersion:     "2.0.0",
		},
		{
			name:            "invalid json",
			packageJson:     `{not valid json}`,
			wantPackageName: "",
			wantVersion:     "",
		},
	}

	h := &NodeHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()

			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(tt.packageJson), 0644); err != nil {
				t.Fatal(err)
			}

			entryFile := filepath.Join(dir, "index.js")
			if err := os.WriteFile(entryFile, []byte(""), 0644); err != nil {
				t.Fatal(err)
			}

			proc := &Process{
				PID:            99999,
				ExecutablePath: "/usr/bin/node",
				Language:       LangNode,
				Details:        make(map[string]any),
			}

			cmdArgs := []string{"node", entryFile}
			h.extractNodeInfo(proc, cmdArgs)

			gotName := proc.DetailString(DetailPackageName)
			if gotName != tt.wantPackageName {
				t.Errorf("DetailPackageName = %q, want %q", gotName, tt.wantPackageName)
			}

			gotVersion := proc.DetailString(DetailPackageVersion)
			if gotVersion != tt.wantVersion {
				t.Errorf("DetailPackageVersion = %q, want %q", gotVersion, tt.wantVersion)
			}
		})
	}
}

func TestExtractNodeInfoNoPackageJson(t *testing.T) {
	dir := t.TempDir()

	h := &NodeHandler{}
	proc := &Process{
		PID:            99999,
		ExecutablePath: "/usr/bin/node",
		Language:       LangNode,
		Details:        make(map[string]any),
	}

	entryFile := filepath.Join(dir, "server.js")
	if err := os.WriteFile(entryFile, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cmdArgs := []string{"node", entryFile}
	h.extractNodeInfo(proc, cmdArgs)

	if got := proc.DetailString(DetailPackageName); got != "" {
		t.Errorf("DetailPackageName = %q, want empty", got)
	}
}

func TestNodeToServiceSettingServiceType(t *testing.T) {
	h := &NodeHandler{}

	// Use a fake PID that won't match any real systemd cgroup, so
	// CheckSystemdStatus returns (false, "") → serviceType = "system".
	const fakePID int32 = 999999

	t.Run("plain system process gets system type", func(t *testing.T) {
		proc := &Process{
			PID:         fakePID,
			Language:    LangNode,
			ServiceName: "my-app",
			Details:     make(map[string]any),
		}
		ss := h.ToServiceSetting(proc)
		if ss.ServiceType != "system" {
			t.Errorf("ServiceType = %q, want %q", ss.ServiceType, "system")
		}
	})

	t.Run("PM2 process gets system type not pm2", func(t *testing.T) {
		proc := &Process{
			PID:         fakePID,
			Language:    LangNode,
			ServiceName: "my-app",
			Details: map[string]any{
				DetailIsPM2:          true,
				DetailProcessManager: "pm2",
			},
		}
		ss := h.ToServiceSetting(proc)
		if ss.ServiceType != "system" {
			t.Errorf("ServiceType = %q, want %q (PM2 should not override)", ss.ServiceType, "system")
		}
	})

	t.Run("forever process gets system type not forever", func(t *testing.T) {
		proc := &Process{
			PID:         fakePID,
			Language:    LangNode,
			ServiceName: "my-app",
			Details: map[string]any{
				DetailIsForever:      true,
				DetailProcessManager: "forever",
			},
		}
		ss := h.ToServiceSetting(proc)
		if ss.ServiceType != "system" {
			t.Errorf("ServiceType = %q, want %q (forever should not override)", ss.ServiceType, "system")
		}
	})

	t.Run("containerized process gets docker type", func(t *testing.T) {
		proc := &Process{
			PID:         fakePID,
			Language:    LangNode,
			ServiceName: "my-app",
			ContainerInfo: &ContainerInfo{
				IsContainer:   true,
				ContainerID:   "abcdef1234567890",
				ContainerName: "my-container",
				Runtime:       "docker",
			},
			Details: make(map[string]any),
		}
		ss := h.ToServiceSetting(proc)
		if ss.ServiceType != "docker" {
			t.Errorf("ServiceType = %q, want %q", ss.ServiceType, "docker")
		}
		if ss.ServiceName != "my-container" {
			t.Errorf("ServiceName = %q, want %q", ss.ServiceName, "my-container")
		}
	})

	t.Run("containerized PM2 process gets docker type not pm2", func(t *testing.T) {
		proc := &Process{
			PID:         fakePID,
			Language:    LangNode,
			ServiceName: "my-app",
			ContainerInfo: &ContainerInfo{
				IsContainer:   true,
				ContainerID:   "abcdef1234567890",
				ContainerName: "pm2-container",
				Runtime:       "docker",
			},
			Details: map[string]any{
				DetailIsPM2:          true,
				DetailProcessManager: "pm2",
			},
		}
		ss := h.ToServiceSetting(proc)
		if ss.ServiceType != "docker" {
			t.Errorf("ServiceType = %q, want %q (container overrides PM2)", ss.ServiceType, "docker")
		}
	})
}
