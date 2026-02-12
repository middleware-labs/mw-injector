package otelinject

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type SystemdDropin struct {
	LdPreload        string `json:"LD_PRELOAD"`
	ServiceName      string `json:"OTEL_SERVICE_NAME"`
	ExporterEndpoint string `json:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	OtlpHeaders      string `json:"OTEL_EXPORTER_OTLP_HEADERS"`
}

func NewSystemdDropin(processPID int32, cleanName string) (*SystemdDropin, error) {
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
	if err := d.validate(); err != nil {
		return fmt.Errorf("invalid drop-in config: %w", err)
	}

	content := fmt.Sprintf(`[Service]
Environment="LD_PRELOAD=%s"
Environment="OTEL_SERVICE_NAME=%s"
Environment="OTEL_EXPORTER_OTLP_ENDPOINT=%s"
Environment="OTEL_EXPORTER_OTLP_HEADERS=%s"
`,
		shellescape(d.LdPreload),
		shellescape(d.ServiceName),
		shellescape(d.ExporterEndpoint),
		shellescape(d.OtlpHeaders),
	)

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

func (d *SystemdDropin) validate() error {
	blocked := []string{"user@", "session-", "init.scope", "dbus"}
	for _, prefix := range blocked {
		if strings.Contains(d.ServiceName, prefix) {
			return fmt.Errorf("refusing to instrument system service: %s", d.ServiceName)
		}
	}

	forbidden := []string{"\n", "\r", "\""}
	fields := map[string]string{
		"ServiceName":      d.ServiceName,
		"LdPreload":        d.LdPreload,
		"ExporterEndpoint": d.ExporterEndpoint,
		"OtlpHeaders":      d.OtlpHeaders,
	}

	for name, value := range fields {
		for _, char := range forbidden {
			if strings.Contains(value, char) {
				return fmt.Errorf("%s contains forbidden character: %q", name, char)
			}
		}
	}
	return nil
}

// Helper function for systemd environment escaping
func shellescape(s string) string {
	// Systemd expects values in the format: Environment="KEY=value"
	// We need to escape quotes and backslashes
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
