package otelinject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddSelectorToEmptyConfig(t *testing.T) {
	cfg := NewEmptyOBIConfig()
	overwritten, err := cfg.AddSelector(OBISelector{
		Name:      "myapp",
		OpenPorts: "8080",
		Languages: "java",
	})
	if err != nil {
		t.Fatalf("AddSelector: %v", err)
	}
	if overwritten {
		t.Fatal("expected overwritten=false for empty config")
	}

	sels := cfg.ListSelectors()
	if len(sels) != 1 {
		t.Fatalf("expected 1 selector, got %d", len(sels))
	}
	if sels[0].Name != "myapp" || sels[0].OpenPorts != "8080" || sels[0].Languages != "java" {
		t.Fatalf("unexpected selector: %+v", sels[0])
	}
}

func TestAddSelectorToExistingConfig(t *testing.T) {
	yamlContent := `
otel_traces_exporter: otlp
discovery:
  instrument:
    - name: existing-app
      open_ports: "3000"
`
	cfg := parseConfigString(t, yamlContent)

	_, err := cfg.AddSelector(OBISelector{
		Name:      "new-app",
		ExePath:   "/usr/bin/myapp",
		Languages: "go",
	})
	if err != nil {
		t.Fatalf("AddSelector: %v", err)
	}

	sels := cfg.ListSelectors()
	if len(sels) != 2 {
		t.Fatalf("expected 2 selectors, got %d", len(sels))
	}
	if sels[1].Name != "new-app" {
		t.Fatalf("expected new-app, got %s", sels[1].Name)
	}
}

func TestOverwriteSelectorByName(t *testing.T) {
	yamlContent := `
discovery:
  instrument:
    - name: myapp
      open_ports: "8080"
`
	cfg := parseConfigString(t, yamlContent)

	overwritten, err := cfg.AddSelector(OBISelector{
		Name:      "myapp",
		OpenPorts: "9090",
		Languages: "java",
	})
	if err != nil {
		t.Fatalf("AddSelector: %v", err)
	}
	if !overwritten {
		t.Fatal("expected overwritten=true")
	}

	sels := cfg.ListSelectors()
	if len(sels) != 1 {
		t.Fatalf("expected 1 selector, got %d", len(sels))
	}
	if sels[0].OpenPorts != "9090" {
		t.Fatalf("expected port 9090, got %s", sels[0].OpenPorts)
	}
}

func TestRemoveSelector(t *testing.T) {
	yamlContent := `
discovery:
  instrument:
    - name: keep-me
      open_ports: "3000"
    - name: remove-me
      open_ports: "8080"
`
	cfg := parseConfigString(t, yamlContent)

	if !cfg.RemoveSelector("remove-me") {
		t.Fatal("expected RemoveSelector to return true")
	}

	sels := cfg.ListSelectors()
	if len(sels) != 1 {
		t.Fatalf("expected 1 selector, got %d", len(sels))
	}
	if sels[0].Name != "keep-me" {
		t.Fatalf("expected keep-me, got %s", sels[0].Name)
	}
}

func TestRemoveSelectorNotFound(t *testing.T) {
	cfg := NewEmptyOBIConfig()
	if cfg.RemoveSelector("nonexistent") {
		t.Fatal("expected RemoveSelector to return false")
	}
}

func TestHasSelector(t *testing.T) {
	yamlContent := `
discovery:
  instrument:
    - name: myapp
      open_ports: "8080"
`
	cfg := parseConfigString(t, yamlContent)
	if !cfg.HasSelector("myapp") {
		t.Fatal("expected HasSelector=true for myapp")
	}
	if cfg.HasSelector("other") {
		t.Fatal("expected HasSelector=false for other")
	}
}

func TestRoundTripPreservesUnrelatedSections(t *testing.T) {
	yamlContent := `otel_traces_exporter: otlp
target_url: https://example.com
discovery:
  instrument:
    - name: existing
      open_ports: "3000"
  poll_interval: 5s
`
	cfg := parseConfigString(t, yamlContent)

	_, err := cfg.AddSelector(OBISelector{Name: "added", OpenPorts: "8080"})
	if err != nil {
		t.Fatalf("AddSelector: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := cfg.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, _ := os.ReadFile(path)
	output := string(data)

	for _, expected := range []string{"otel_traces_exporter", "target_url", "poll_interval", "existing", "added"} {
		if !strings.Contains(output, expected) {
			t.Errorf("output missing %q:\n%s", expected, output)
		}
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := NewEmptyOBIConfig()
	cfg.AddSelector(OBISelector{Name: "test", OpenPorts: "8080"})

	if err := cfg.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify file exists and is readable
	reloaded, err := ReadOBIConfig(path)
	if err != nil {
		t.Fatalf("ReadOBIConfig after write: %v", err)
	}
	sels := reloaded.ListSelectors()
	if len(sels) != 1 || sels[0].Name != "test" {
		t.Fatalf("unexpected selectors after round-trip: %+v", sels)
	}
}

func TestAddSelectorNoDiscoverySection(t *testing.T) {
	yamlContent := `otel_traces_exporter: otlp
target_url: https://example.com
`
	cfg := parseConfigString(t, yamlContent)

	_, err := cfg.AddSelector(OBISelector{Name: "myapp", OpenPorts: "8080"})
	if err != nil {
		t.Fatalf("AddSelector: %v", err)
	}

	sels := cfg.ListSelectors()
	if len(sels) != 1 || sels[0].Name != "myapp" {
		t.Fatalf("unexpected: %+v", sels)
	}
}

// --- helpers ---

func parseConfigString(t *testing.T, content string) *OBIConfig {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfg, err := ReadOBIConfig(path)
	if err != nil {
		t.Fatalf("ReadOBIConfig: %v", err)
	}
	return cfg
}
