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
