package discovery

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"time"
)

const apiPathForAgentSetting = "api/v1/agent/public/setting/"

// ServiceSetting represents the detailed status for a single service/process.
type ServiceSetting struct {
	PID               int    `json:"pid"`
	ServiceName       string `json:"service_name"`
	Owner             string `json:"owner"`
	Status            string `json:"status"`
	Enabled           bool   `json:"enabled"`
	ServiceType       string `json:"service_type"`
	Language          string `json:"language"`
	RuntimeVersion    string `json:"runtime_version"`
	SystemdUnit       string `json:"systemd_unit,omitempty"`
	JarFile           string `json:"jar_file,omitempty"`
	MainClass         string `json:"main_class,omitempty"`
	HasAgent          bool   `json:"has_agent"`
	IsMiddlewareAgent bool   `json:"is_middleware_agent"`
	AgentPath         string `json:"agent_path,omitempty"`
	ConfigPath        string `json:"config_path,omitempty"`
	Instrumented      bool   `json:"instrumented"`
	Key               string `json:"key"`
}

// OSConfig represents the configuration and status for a specific OS (e.g., "linux").
type OSConfig struct {
	AgentRestartStatus          bool                      `json:"agent_restart_status"`
	AutoInstrumentationInit     bool                      `json:"auto_instrumentation_init"`
	AutoInstrumentationSettings map[string]ServiceSetting `json:"auto_instrumentation_settings"`
	// Add other OS-specific fields (darwin, windows, k8s, etc.) if needed
}

// AgentReportValue is the root structure for the 'value' field's JSON content.
type AgentReportValue map[string]OSConfig

// type Metadata struct {
// 	AgentSetting  string
// 	Platform      string
// 	InfraPlatform string
// 	// ColRunning    *otelcol.Collector
// }

type AgentSettingPayload struct {
	Value    string                            `json:"value"` // Base64 encoded config
	MetaData map[string]interface{}            `json:"meta_data"`
	Config   map[string]map[string]interface{} `json:"config"` // This field is technically redundant based on the API handler's logic but included for completeness if needed.
}

func GetAgentReportValue() (AgentReportValue, error) {

	// --- 1. Perform Process Discovery ---
	ctx := context.Background()

	// a) Host Processes (Java)
	processes, err := FindAllJavaProcesses(ctx)
	if err != nil {
		// c.logger.Error("Failed to discover host Java processes", zap.Error(err))
		// Decide if this should be a fatal error or just logged (assuming logged for now)
	}

	nodeProcs, err := FindAllNodeProcesses(ctx)
	// pp.Println(nodeProcs)
	// --- 2. Convert to AgentReportValue (ServiceSetting) ---
	osKey := runtime.GOOS
	settings := map[string]ServiceSetting{}

	// Convert host processes
	for _, proc := range processes {
		// Only report processes we care about (non-Tomcat, non-Container for simplicity)
		// if !proc.IsTomcat() && !proc.ContainerInfo.IsContainer {
		if !proc.IsTomcat() {
			if proc.IsInContainer() {
			}
			setting := convertJavaProcessToServiceSetting(proc)
			settings[setting.Key] = setting
		}
	}

	pythonProcs, _ := FindAllPythonProcess(ctx)
	for _, proc := range pythonProcs {
		setting := convertPythonProcessToServiceSetting(proc)
		settings[setting.Key] = setting
	}
	for _, proc := range nodeProcs {
		setting := convertNodeProcessToServiceSetting(proc)
		settings[setting.Key] = setting
	}

	reportValue := AgentReportValue{
		osKey: OSConfig{
			AgentRestartStatus:          false,
			AutoInstrumentationInit:     true,
			AutoInstrumentationSettings: settings,
		},
	}

	return reportValue, nil
}

// Placeholder for logic that converts discovery.Container to ServiceSetting
func convertJavaContainerToServiceSetting(container DockerContainer) ServiceSetting {
	// Generate a unique key for the container service
	// key := container.ContainerID[:12] // Use short ID
	key := container.ContainerID[:12]
	// You can access the embedded JavaProcess like this: container.JavaProcess

	return ServiceSetting{
		ServiceName: container.ContainerName,
		// ... fill other fields from container.ContainerInfo and container.JavaProcess ...
		ServiceType: "docker",
		Language:    "java",
		Key:         key,
	}
}

func convertNodeProcessToServiceSetting(proc NodeProcess) ServiceSetting {
	// 1. Generate a stable Key
	key := fmt.Sprintf("host-%d", proc.ProcessPID)
	serviceType := "system"

	// 2. Handle Container Infrastructure
	if proc.IsInContainer() {
		serviceType = "docker"
		if proc.ContainerInfo.ContainerID != "" {
			// Use Container ID for the key to prevent PID collisions
			key = fmt.Sprintf("docker-node-%s", proc.ContainerInfo.ContainerID[:12])
			proc.ServiceName = proc.ContainerInfo.ContainerName
		}
	}
  return ServiceSetting{
      PID:               int(proc.ProcessPID),
      ServiceName:       proc.ServiceName,
      Owner:             proc.ProcessOwner,
      Status:            proc.Status,
      Enabled:           true,
      ServiceType:       serviceType, // Now correctly reports "docker"
      Language:          "node-js",
      RuntimeVersion:    proc.ProcessRuntimeVersion,
      HasAgent:          proc.HasNodeAgent,
      IsMiddlewareAgent: proc.IsMiddlewareAgent,
      AgentPath:         proc.NodeAgentPath,
      Instrumented:      proc.HasNodeAgent,
      Key:               key,
    }
}

func convertPythonProcessToServiceSetting(proc PythonProcess) ServiceSetting {
	// Generate a unique key for the service
	key := fmt.Sprintf("host-%d", proc.ProcessPID)

	// Determine the service type based on process manager or environment
	serviceType := "standalone"

	if proc.IsInContainer() {
		serviceType = "docker"
	} else if proc.IsGunicornProcess || proc.IsUvicornProcess {
		serviceType = "wsgi/asgi"
	} else if proc.IsCeleryProcess {
		serviceType = "worker"
	}

	return ServiceSetting{
		PID:            int(proc.ProcessPID),
		ServiceName:    proc.ServiceName,
		Owner:          proc.ProcessOwner,
		Status:         proc.Status,
		Enabled:        true, // Discovered processes are instrumentation candidates
		ServiceType:    serviceType,
		Language:       "python",
		RuntimeVersion: proc.ProcessRuntimeVersion,

		// Python specific "Main" info
		MainClass: proc.ModulePath, // If using -m
		JarFile:   proc.EntryPoint, // If using script.py

		// Instrumentation Status
		HasAgent:          proc.HasPythonAgent,
		IsMiddlewareAgent: proc.IsMiddlewareAgent,
		AgentPath:         proc.PythonAgentPath,
		Instrumented:      proc.HasPythonAgent,

		// Metadata and Unique Key
		Key: key,
	}
}

func convertNodeContainerToServiceSetting(container DockerContainer) ServiceSetting {
	// Generate a unique key for the container service. We'll use the container's short ID.
	containerID := container.ContainerID
	key := ""
	if len(containerID) >= 12 {
		key = containerID[:12] // Use short ID as key
	} else {
		key = containerID // Fallback if ID is too short
	}

	// Determine if the container is currently instrumented.
	// The Container struct has an IsInstrumented field.
	isInstrumented := container.Instrumented

	// Determine agent path (specific to Node.js agent, if instrumented)
	agentPath := ""
	if isInstrumented {
		// Assume the Node agent path is known or retrievable from the container struct's details
		// For a clean conversion, we'll use a placeholder or check a specific field if available.
		// If the discovery struct doesn't expose the path, we infer a default or leave blank.
		agentPath = container.NodeAgentPath // Assuming this field exists on the discovery.Container struct
		if agentPath == "" {
			agentPath = "/opt/opentelemetry/node_agent" // Common default location
		}
	}

	return ServiceSetting{
		// PID is not always relevant or stable for containers, often left 0 or 1
		PID:            0,
		ServiceName:    container.ContainerName,
		Status:         "running", // Containers are assumed running if discovered
		Enabled:        true,      // Available for instrumentation
		ServiceType:    "docker",
		Language:       "nodejs",
		RuntimeVersion: "", // Version often hard to determine from outside container, leave empty or look up

		// Tomcat/Systemd specific fields are omitted for Docker/Node
		SystemdUnit: "",
		JarFile:     "",
		MainClass:   "",

		HasAgent:          isInstrumented,
		IsMiddlewareAgent: isInstrumented, // Assuming only Middleware agent is tracked
		AgentPath:         agentPath,
		Instrumented:      isInstrumented,
		Key:               fmt.Sprintf("docker-node-%s", key), // Unique and descriptive key prefix
	}
}

// Placeholder for logic that converts discovery.JavaProcess to ServiceSetting
func convertJavaProcessToServiceSetting(proc JavaProcess) ServiceSetting {
	// Generate a unique key for the service. The naming package helps here.
	// e.g., key := naming.GenerateHostServiceKey(proc.ServiceName, "systemd", proc.PID)
	key := fmt.Sprintf("host-%d", proc.ProcessPID)

	return ServiceSetting{
		PID:               int(proc.ProcessPID),
		ServiceName:       proc.ServiceName, // Uses the discovered ServiceName
		Owner:             proc.ProcessOwner,
		Status:            proc.Status,
		Enabled:           true,                        // Assuming discovery means it's available for instrumentation
		ServiceType:       detectDeploymentType(&proc), // Need your detectDeploymentType helper from the ListAllCommand!
		Language:          "java",
		RuntimeVersion:    proc.ProcessRuntimeVersion,
		JarFile:           proc.JarFile,
		MainClass:         proc.MainClass,
		HasAgent:          proc.HasJavaAgent,
		IsMiddlewareAgent: proc.IsMiddlewareAgent,
		AgentPath:         proc.JavaAgentPath,
		Instrumented:      proc.HasJavaAgent, // Can be refined
		Key:               key,
	}
}

func detectDeploymentType(proc *JavaProcess) string {
	if proc.ProcessOwner != "root" && proc.ProcessOwner != os.Getenv("USER") {
		return "systemd"
	}
	if proc.IsInContainer() {
		return "docker"
	}
	return "standalone"
}

func ReportStatus(
	hostname string,
	apiKey string,
	urlForConfigCheck string,
	version string,
	infraPlatform string,
) error {
	u, err := url.Parse(urlForConfigCheck)
	if err != nil {
		return err
	}
	baseURL := u.JoinPath(apiPathForAgentSetting, apiKey, hostname)
	finalURL := baseURL.String()

	rawReportValue, err := GetAgentReportValue()
	if err != nil {
		return fmt.Errorf("failed to generate agent report value: %w", err)
	}

	rawConfigBytes, err := json.Marshal(rawReportValue)
	if err != nil {
		return fmt.Errorf("failed to marshal raw config payload: %w", err)
	}

	encodedConfig := base64.StdEncoding.EncodeToString(rawConfigBytes)

	payload := AgentSettingPayload{
		Value: encodedConfig,
		MetaData: map[string]interface{}{
			"agent_version":  version,
			"platform":       runtime.GOOS,
			"infra_platform": fmt.Sprint(infraPlatform),
			// c.collector == nil means the collector is NOT running (i.e., collectorRunning = 1)
		},
		// Config field is set to nil as per backend API pattern unless needed
		Config: nil,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal final request payload: %w", err)
	}

	// 4. Create and Execute the HTTP POST Request
	req, err := http.NewRequest(http.MethodPost, finalURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("agent status POST request failed for %s: %w", finalURL, err)
	}
	defer resp.Body.Close()

	// 5. Check Status Code
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent status POST API returned non-200 status code: %d", resp.StatusCode)
	}

	return nil
}
