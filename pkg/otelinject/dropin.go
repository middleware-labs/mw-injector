package otelinject

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/k0kubun/pp"
)

type SystemdDropin struct {
	LdPreload        string `json:"LD_PRELOAD"`
	ServiceName      string `json:"OTEL_SERVICE_NAME"`
	ExporterEndpoint string `json:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	OtlpHeaders      string `json:"OTEL_EXPORTER_OTLP_HEADERS"`
}

func NewSystemdDropin(processPID int32) (*SystemdDropin, error) {
	pp.Println("Trying to create drop")

	path := fmt.Sprintf("/proc/%d/cgroup", processPID) // Input
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read cgroup: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	serviceName := extractServiceNameFromCgroup(lines)
	if serviceName == "" {
		return nil, fmt.Errorf("could not extract service name from cgroup, cannot create drop-in")
	}

	cleanName := strings.TrimSuffix(serviceName, ".service")

	// Get values from Environment
	apiKey := os.Getenv("MW_API_KEY")
	target := os.Getenv("MW_TARGET")

	return &SystemdDropin{
		LdPreload:        DefaultLibOtelInjectorPath,
		ServiceName:      cleanName,
		ExporterEndpoint: target,
		OtlpHeaders:      fmt.Sprintf("Authorization=%s", apiKey),
	}, nil
}

func (d *SystemdDropin) applySystemdDropIn() error {
	// 1. Prepare the content using the struct fields
	// We use %q (quoted string) to ensure values are safely wrapped in quotes.
	content := fmt.Sprintf(`[Service]
Environment="LD_PRELOAD=%s"
Environment="OTEL_SERVICE_NAME=%s"
Environment="OTEL_EXPORTER_OTLP_ENDPOINT=%s"
Environment="OTEL_EXPORTER_OTLP_HEADERS=%s"
`, d.LdPreload, d.ServiceName, d.ExporterEndpoint, d.OtlpHeaders)

	// 2. Setup Directory: /etc/systemd/system/<service>.d/
	dropInDir := fmt.Sprintf("/etc/systemd/system/%s.service.d", d.ServiceName)
	if err := os.MkdirAll(dropInDir, 0755); err != nil {
		return fmt.Errorf("failed to create drop-in dir: %w", err)
	}

	// 3. Write File
	filename := filepath.Join(dropInDir, "middleware-otel.conf")
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write drop-in file: %w", err)
	}

	pp.Printf("Written drop-in file for %s.service at %s\n", d.ServiceName, filename)

	// 4. Reload Daemon
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload failed: %s: %w", string(out), err)
	}

	// 5. Restart Service
	if out, err := exec.Command("systemctl", "restart", "--no-block", fmt.Sprintf("%s.service", d.ServiceName)).CombinedOutput(); err != nil {
		return fmt.Errorf("service restart failed: %s: %w", string(out), err)
	}

	return nil
}
