package config

import (
	"fmt"
	"strings"
	"time"
)

// ProcessConfiguration represents MW environment variables for a Java process
type ProcessConfiguration struct {
	// Process identification
	PID         int32  `json:"pid"`
	ServiceName string `json:"service_name"`
	JarFile     string `json:"jar_file"`

	// Core MW settings (required)
	MWAPIKey      string `json:"mw_api_key" yaml:"MW_API_KEY"`
	MWTarget      string `json:"mw_target" yaml:"MW_TARGET"`
	MWServiceName string `json:"mw_service_name" yaml:"MW_SERVICE_NAME"`

	// Feature toggles
	MWAPMCollectProfiling bool `json:"mw_apm_collect_profiling" yaml:"MW_APM_COLLECT_PROFILING"`
	MWAPMCollectTraces    bool `json:"mw_apm_collect_traces" yaml:"MW_APM_COLLECT_TRACES"`
	MWAPMCollectLogs      bool `json:"mw_apm_collect_logs" yaml:"MW_APM_COLLECT_LOGS"`
	MWAPMCollectMetrics   bool `json:"mw_apm_collect_metrics" yaml:"MW_APM_COLLECT_METRICS"`

	// Network settings
	MWEnableGzip   bool   `json:"mw_enable_gzip" yaml:"MW_ENABLE_GZIP"`
	MWAuthURL      string `json:"mw_auth_url" yaml:"MW_AUTH_URL"`
	MWAgentService string `json:"mw_agent_service" yaml:"MW_AGENT_SERVICE"`
	MWPropagators  string `json:"mw_propagators" yaml:"MW_PROPAGATORS"`

	// Profiling settings
	MWProfilingServerURL string `json:"mw_profiling_server_url" yaml:"MW_PROFILING_SERVER_URL"`
	MWProfilingAlloc     string `json:"mw_profiling_alloc" yaml:"MW_PROFILING_ALLOC"`
	MWProfilingLock      string `json:"mw_profiling_lock" yaml:"MW_PROFILING_LOCK"`

	// Additional settings
	MWLogLevel                string `json:"mw_log_level" yaml:"MW_LOG_LEVEL"`
	MWCustomResourceAttribute string `json:"mw_custom_resource_attribute" yaml:"MW_CUSTOM_RESOURCE_ATTRIBUTE"`
	MWDisableTelemetry        bool   `json:"mw_disable_telemetry" yaml:"MW_DISABLE_TELEMETRY"`

	// OTEL settings (passed as -D flags)
	OtelServiceName        string `json:"otel_service_name"`
	OtelResourceAttributes string `json:"otel_resource_attributes"` // project.name=X
	OtelTracesSampler      string `json:"otel_traces_sampler"`
	OtelTracesSamplerArg   string `json:"otel_traces_sampler_arg"`

	// Java agent path
	JavaAgentPath string `json:"java_agent_path"`

	NodeAgentPath string `json:"node_agent_path"`
	// Container settings
	IsContainer   bool   `json:"is_container"`
	ContainerType string `json:"container_type"` // docker, kubernetes

	// Metadata
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DefaultConfiguration returns sensible defaults based on MW documentation
func DefaultConfiguration() ProcessConfiguration {
	return ProcessConfiguration{
		// Defaults from documentation
		MWAPMCollectProfiling: true,
		MWAPMCollectTraces:    true,
		MWAPMCollectLogs:      true,
		MWAPMCollectMetrics:   true,
		MWEnableGzip:          true,
		MWAuthURL:             "https://app.middleware.io/api/v1/auth",
		MWAgentService:        "localhost",
		MWPropagators:         "b3",
		MWProfilingAlloc:      "512k",
		MWProfilingLock:       "10ms",
		MWLogLevel:            "INFO",

		// OTEL defaults
		OtelTracesSampler:    "parentbased_always_on",
		OtelTracesSamplerArg: "1.0",

		// Metadata
		Enabled: true,
	}
}

// Validate checks if required fields are set
func (c *ProcessConfiguration) Validate() error {
	if c.MWAPIKey == "" {
		return fmt.Errorf("MW_API_KEY is required")
	}
	if c.MWServiceName == "" {
		return fmt.Errorf("MW_SERVICE_NAME is required")
	}
	return nil
}

// ToEnvironmentVariables converts config to environment variable format
func (c *ProcessConfiguration) ToEnvironmentVariables() map[string]string {
	env := make(map[string]string)

	// Required settings
	env["MW_API_KEY"] = c.MWAPIKey
	if c.MWTarget != "" {
		env["MW_TARGET"] = c.MWTarget
	}

	if c.MWServiceName != "" {
		env["MW_SERVICE_NAME"] = c.MWServiceName
	}

	// Feature toggles
	env["MW_APM_COLLECT_PROFILING"] = boolToString(c.MWAPMCollectProfiling)
	env["MW_APM_COLLECT_TRACES"] = boolToString(c.MWAPMCollectTraces)
	env["MW_APM_COLLECT_LOGS"] = boolToString(c.MWAPMCollectLogs)
	env["MW_APM_COLLECT_METRICS"] = boolToString(c.MWAPMCollectMetrics)

	// Network settings
	env["MW_ENABLE_GZIP"] = boolToString(c.MWEnableGzip)
	if c.MWAuthURL != "" {
		env["MW_AUTH_URL"] = c.MWAuthURL
	}
	if c.MWAgentService != "" {
		env["MW_AGENT_SERVICE"] = c.MWAgentService
	}

	if c.OtelServiceName != "" {
		env["OTEL_SERVICE_NAME"] = c.OtelServiceName
	}

	if c.MWPropagators != "" {
		env["MW_PROPAGATORS"] = c.MWPropagators
	}

	// Profiling
	if c.MWProfilingServerURL != "" {
		env["MW_PROFILING_SERVER_URL"] = c.MWProfilingServerURL
	}
	if c.MWProfilingAlloc != "" {
		env["MW_PROFILING_ALLOC"] = c.MWProfilingAlloc
	}
	if c.MWProfilingLock != "" {
		env["MW_PROFILING_LOCK"] = c.MWProfilingLock
	}

	// Additional
	if c.MWLogLevel != "" {
		env["MW_LOG_LEVEL"] = c.MWLogLevel
	}
	if c.MWCustomResourceAttribute != "" {
		env["MW_CUSTOM_RESOURCE_ATTRIBUTE"] = c.MWCustomResourceAttribute
	}
	if c.MWDisableTelemetry {
		env["MW_DISABLE_TELEMETRY"] = "true"
	}

	return env
}

// ToJavaCommandLine generates the java command line with all settings
func (c *ProcessConfiguration) ToJavaCommandLine(jarFile string) string {
	parts := []string{}

	// Add environment variables
	for k, v := range c.ToEnvironmentVariables() {
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", k, v))
	}

	// Add java command with agent
	javaParts := []string{"java"}

	if c.JavaAgentPath != "" {
		javaParts = append(javaParts, fmt.Sprintf("-javaagent:%s", c.JavaAgentPath))
	}

	// Add OTEL system properties
	if c.OtelServiceName != "" {
		javaParts = append(javaParts, fmt.Sprintf("-Dotel.service.name=%s", c.OtelServiceName))
	}
	if c.OtelResourceAttributes != "" {
		javaParts = append(javaParts, fmt.Sprintf("-Dotel.resource.attributes=%s", c.OtelResourceAttributes))
	}
	if c.OtelTracesSampler != "" {
		javaParts = append(javaParts, fmt.Sprintf("-Dotel.traces.sampler=%s", c.OtelTracesSampler))
	}
	if c.OtelTracesSamplerArg != "" {
		javaParts = append(javaParts, fmt.Sprintf("-Dotel.traces.sampler.arg=%s", c.OtelTracesSamplerArg))
	}

	javaParts = append(javaParts, "-jar", jarFile)

	parts = append(parts, strings.Join(javaParts, " \\\n    "))

	return strings.Join(parts, " \\\n")
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
