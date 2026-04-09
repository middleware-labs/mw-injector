package discovery

import (
	"fmt"
	"regexp"
	"strings"
)

// extractLibOtelInjectPath extracts the libotelinject.so path from a LD_PRELOAD value.
// LD_PRELOAD can be colon-separated, e.g. /usr/lib/custom.so:/usr/lib/opentelemetry/libotelinject.so
func extractLibOtelInjectPath(ldPreload string) string {
	for _, p := range strings.Split(ldPreload, ":") {
		if strings.Contains(p, "libotelinject.so") {
			return p
		}
	}
	return ""
}

// GetAgentInfo returns detailed information about the detected agent
func (jp *JavaProcess) GetAgentInfo() *AgentInfo {
	if !jp.HasJavaAgent {
		return &AgentInfo{Type: AgentNone}
	}

	agentType := AgentOther
	version := "unknown"
	isServerless := false

	if jp.IsMiddlewareAgent {
		agentType = AgentMiddleware
		version = extractMWAgentVersion(jp.JavaAgentPath)
		isServerless = isMWServerlessAgent(jp.JavaAgentPath)
	} else {
		detectedType := detectJavaAgentType(jp.JavaAgentPath)
		if detectedType != AgentOther {
			agentType = detectedType
		}
	}

	return &AgentInfo{
		Type:         agentType,
		Path:         jp.JavaAgentPath,
		Name:         jp.JavaAgentName,
		Version:      version,
		IsServerless: isServerless,
	}
}

// FormatAgentStatus returns a human-readable agent status string
func (jp *JavaProcess) FormatAgentStatus() string {
	var status string

	if !jp.HasJavaAgent {
		status = "[x] None"
	} else {
		agentInfo := jp.GetAgentInfo()
		switch agentInfo.Type {
		case AgentMiddleware:
			if agentInfo.IsServerless {
				status = "[ok] MW (Serverless)"
			} else {
				status = "[ok] MW"
			}
		case AgentOpenTelemetry:
			status = "[ok] OTel"
		case AgentOther:
			status = "[ok] Other"
		default:
			status = "[?] Unknown"
		}
	}

	if jp.IsInContainer() {
		status += fmt.Sprintf(" (container: %s)", jp.GetContainerRuntime())
	}

	return status
}

// HasInstrumentation checks if the process has any form of instrumentation
func (jp *JavaProcess) HasInstrumentation() bool {
	return jp.HasJavaAgent
}

// HasMiddlewareInstrumentation checks specifically for Middleware instrumentation
func (jp *JavaProcess) HasMiddlewareInstrumentation() bool {
	return jp.IsMiddlewareAgent
}

// IsServerless checks if this appears to be a serverless deployment
func (jp *JavaProcess) IsServerless() bool {
	if !jp.HasJavaAgent {
		return false
	}
	return jp.GetAgentInfo().IsServerless
}

// --- Private helpers ---

func extractMWAgentVersion(agentPath string) string {
	versionPatterns := []*regexp.Regexp{
		regexp.MustCompile(`-(\d+\.\d+\.\d+)`),
		regexp.MustCompile(`_(\d+\.\d+\.\d+)`),
		regexp.MustCompile(`(\d+\.\d+\.\d+)\.jar`),
		regexp.MustCompile(`v(\d+\.\d+\.\d+)`),
	}

	for _, pattern := range versionPatterns {
		matches := pattern.FindStringSubmatch(agentPath)
		if len(matches) > 1 {
			return matches[1]
		}
	}
	return "unknown"
}

func isMWServerlessAgent(agentPath string) bool {
	lower := strings.ToLower(agentPath)
	serverlessPatterns := []string{"serverless", "lambda", "aws", "wo.healthcheck"}
	for _, p := range serverlessPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

