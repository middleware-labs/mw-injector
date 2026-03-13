package discovery

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/k0kubun/pp"
)

// ServiceSetting represents the detailed status for a single service/process.
type ServiceSetting struct {
	PID               int32  `json:"pid"`
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
	InstrumentThis    bool   `json:"instrument_this"` // I want this to default to false.
	ProcessManager    string `json:"process_manager,omitempty"`
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

type AgentSettingPayload struct {
	Value    string                            `json:"value"` // Base64 encoded config
	MetaData map[string]interface{}            `json:"meta_data"`
	Config   map[string]map[string]interface{} `json:"config"` // This field is technically redundant based on the API handler's logic but included for completeness if needed.
}

func GetAgentReportValue() (AgentReportValue, error) {
	// --- 1. Perform Process Discovery ---
	ctx := context.Background()

	// a) Host Processes (Java)
	// Java has its own optimization from your previous PR
	processes, err := FindAllJavaProcesses(ctx)
	if err != nil {
		fmt.Printf("Error discovering Java processes: %v\n", err)
	}

	// b) Node Processes
	// Internally calls GetCachedProcessMetadata -> Updates LastSeen timestamp
	nodeProcs, err := FindAllNodeProcesses(ctx)
	if err != nil {
		fmt.Printf("Error discovering Node processes: %v\n", err)
	}

	// c) Python Processes
	// Internally calls GetCachedProcessMetadata -> Updates LastSeen timestamp
	pythonProcs, err := FindAllPythonProcess(ctx)
	if err != nil {
		fmt.Printf("Error discovering Python processes: %v\n", err)
	}

	// --- 2. SWEEP (Garbage Collection) ---
	// Simple call. No arguments.
	// It automatically removes any process not seen in the last 10 minutes.
	PruneProcessCache()
	PruneContainerNameCache()

	// --- 3. Convert to AgentReportValue (Reporting) ---
	osKey := runtime.GOOS
	settings := map[string]ServiceSetting{}

	// Convert Java
	for _, proc := range processes {
		if !proc.IsTomcat() {
			setting := convertJavaProcessToServiceSetting(proc)
			settings[setting.Key] = setting
		}
	}

	// Convert Python
	for _, proc := range pythonProcs {
		setting := convertPythonProcessToServiceSetting(proc)
		settings[setting.Key] = setting
	}

	// Convert Node
	for _, proc := range nodeProcs {
		setting := convertNodeProcessToServiceSetting(proc)
		settings[setting.Key] = setting
	}

	pp.Println("lets see the unit files")
	for _, proc := range settings {
		pp.Println("pid: ", proc.PID, "unitName: ", proc.SystemdUnit)
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

	key := fmt.Sprintf("host-node-%s", sanitize(proc.ServiceName))
	serviceType := "system"

	_, unitname := CheckSystemdStatus(proc.ProcessPID)
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
		PID:               proc.ProcessPID,
		ServiceName:       proc.ServiceName,
		Owner:             proc.ProcessOwner,
		Status:            proc.Status,
		Enabled:           true,
		ServiceType:       serviceType, // Now correctly reports "docker"
		Language:          "node",
		RuntimeVersion:    proc.ProcessRuntimeVersion,
		HasAgent:          proc.HasNodeAgent,
		IsMiddlewareAgent: proc.IsMiddlewareAgent,
		AgentPath:         proc.NodeAgentPath,
		Instrumented:      proc.HasNodeAgent,
		Key:               key,
		SystemdUnit:       unitname,
	}
}

func convertPythonProcessToServiceSetting(proc PythonProcess) ServiceSetting {
	// Generate a unique key for the service

	key := fmt.Sprintf("host-python-%s", sanitize(proc.ServiceName))
	// Determine the service type based on process manager or environment
	serviceType := "system"

	_, unitname := CheckSystemdStatus(proc.ProcessPID)

	if proc.IsInContainer() {
		serviceType = "docker"
	} else if proc.IsCeleryProcess {
		serviceType = "worker"
	}

	return ServiceSetting{
		PID:            proc.ProcessPID,
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
		Key:            key,
		ProcessManager: proc.ProcessManager,
		SystemdUnit:    unitname,
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

	key := fmt.Sprintf("host-java-%s", sanitize(proc.ServiceName))
	_, unitname := CheckSystemdStatus(proc.ProcessPID)
	return ServiceSetting{
		PID:               proc.ProcessPID,
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
		SystemdUnit:       unitname,
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

func FilterServices(services map[string]ServiceSetting, predicate func(ServiceSetting) bool) map[string]ServiceSetting {
	result := make(map[string]ServiceSetting)
	for k, v := range services {
		if predicate(v) {
			result[k] = v
		}
	}
	return result
}

func And(predicates ...func(ServiceSetting) bool) func(ServiceSetting) bool {
	return func(s ServiceSetting) bool {
		for _, p := range predicates {
			if !p(s) {
				return false
			}
		}
		return true
	}
}

func FilterInstrumentable(
	storedSettings map[string]ServiceSetting, // Coming from the database
	currentSettings map[string]ServiceSetting, // Coming from the live system
) map[string]ServiceSetting {
	// Build a lookup index from current settings keyed by (service_name, language)
	// since PIDs change across restarts, making key-based matching unreliable.
	type serviceIdentity struct {
		ServiceName string
		Language    string
	}
	currentIndex := make(map[serviceIdentity]ServiceSetting, len(currentSettings))
	for _, current := range currentSettings {
		id := serviceIdentity{current.ServiceName, current.Language}
		currentIndex[id] = current
	}

	result := make(map[string]ServiceSetting)
	for _, stored := range storedSettings {
		if !stored.InstrumentThis {
			continue
		}

		id := serviceIdentity{stored.ServiceName, stored.Language}
		current, exists := currentIndex[id]
		if !exists {
			// Was marked for instrumentation but is no longer running
			continue
		}

		// Use current (live) entry for fresh PID/status/paths,
		// but carry over InstrumentThis from the stored setting
		current.InstrumentThis = true
		result[current.Key] = current
	}

	return result
}

func sanitize(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, " ", "-"))
}

func CheckSystemdStatus(pid int32) (bool, string) {
	path := fmt.Sprintf("/proc/%d/cgroup", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		// Only look at the main systemd hierarchy or unified hierarchy (0::)
		if !strings.Contains(line, ":name=systemd:") && !strings.HasPrefix(line, "0::") {
			continue
		}

		// Extract the path part (everything after the second colon)
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		cgroupPath := parts[2]

		// Split path into segments: /, user.slice, user@1000.service, app.slice, my-app.service
		segments := strings.Split(cgroupPath, "/")

		// REVERSE SEARCH: Find the *last* segment ending in .service
		for i := len(segments) - 1; i >= 0; i-- {
			segment := segments[i]
			if strings.HasSuffix(segment, ".service") {
				// FILTER: Ignore the generic user session service
				if strings.HasPrefix(segment, "user@") {
					continue
				}

				// Found a real service!
				unitName := strings.TrimSuffix(segment, ".service")
				return true, unitName
			}
		}
	}
	return false, ""
}
