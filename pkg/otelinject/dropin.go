// dropin.go creates and removes systemd drop-in configuration files that
// inject OTel environment variables (LD_PRELOAD, OTEL_SERVICE_NAME, etc.)
// into service units. Drop-in files are placed at:
//
//	/etc/systemd/system/{unit}.service.d/middleware-otel.conf
//
// After writing, systemd is reloaded and the service restarted.
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

func NewSystemdDropin(cleanName string) (*SystemdDropin, error) {
	// Get values from Environment
	apiKey := os.Getenv("MW_API_KEY")
	target := os.Getenv("MW_TARGET")

	// Normalize: strip .service suffix so path building is consistent
	// regardless of whether callers pass "flask-app" or "flask-app.service"
	unitName := strings.TrimSuffix(cleanName, ".service")

	return &SystemdDropin{
		LdPreload:        DefaultLibOtelInjectorPath,
		ServiceName:      unitName,
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
	if out, err := exec.Command("systemctl", "restart", "--no-block", fmt.Sprintf("%s", d.ServiceName)).CombinedOutput(); err != nil {
		return fmt.Errorf("service restart failed: %s: %w", string(out), err)
	}

	return nil
}

func (d *SystemdDropin) applySystemdDropInPython() error {
	if err := d.validate(); err != nil {
		return fmt.Errorf("invalid drop-in config: %w", err)
	}

	content := fmt.Sprintf(`[Service]
Environment="LD_PRELOAD=%s"
Environment="PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX=%s"
Environment="OTEL_SERVICE_NAME=%s"
Environment="OTEL_EXPORTER_OTLP_ENDPOINT=%s"
Environment="OTEL_EXPORTER_OTLP_HEADERS=%s"
Environment="OTEL_TRACES_EXPORTER=otlp_proto_http"
Environment="OTEL_METRICS_EXPORTER=otlp_proto_http"
Environment="OTEL_LOGS_EXPORTER=otlp_proto_http"

# Debug logging
Environment="OTEL_INJECTOR_LOG_LEVEL=info"
`,
		shellescape(d.LdPreload),
		shellescape(DefaultPythonAgentBasePath),
		shellescape(d.ServiceName),
		shellescape(d.ExporterEndpoint),
		shellescape(d.OtlpHeaders),
	)

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
	if out, err := exec.Command("systemctl", "restart", "--no-block", d.ServiceName).CombinedOutput(); err != nil {
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

func removeSystemdDropIn(serviceName string) error {
	serviceName = strings.TrimSuffix(serviceName, ".service")
	dropInDir := fmt.Sprintf("/etc/systemd/system/%s.service.d", serviceName)
	dropInPath := filepath.Join(dropInDir, "middleware-otel.conf")
	if _, err := os.Stat(dropInPath); os.IsNotExist(err) {
		return nil // already uninstrumented, nothing to do
	} else if err != nil {
		return fmt.Errorf("failed to stat drop-in for %s: %w", serviceName, err)
	}

	if err := os.Remove(dropInPath); err != nil {
		return fmt.Errorf("failed to remove drop-in file: %w", err)
	}

	// Remove directory if empty
	files, err := os.ReadDir(dropInDir)
	if err != nil {
		return fmt.Errorf("error in reading the dropin dir %v: %w", dropInDir, err)
	}
	if len(files) == 0 {
		err := os.Remove(dropInDir)
		if err != nil {
			return fmt.Errorf("error in removing the dropin directory: %w", err)
		}
	}

	// Reload and restart
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload failed: %s: %w", string(out), err)
	}

	if out, err := exec.Command("systemctl", "restart", "--no-block", fmt.Sprintf("%s", serviceName)).CombinedOutput(); err != nil {
		return fmt.Errorf("service restart failed: %s: %w", string(out), err)
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
