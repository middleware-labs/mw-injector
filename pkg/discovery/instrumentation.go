// instrumentation.go defines agent type classification (Middleware, OpenTelemetry,
// Other), ServiceSetting for backend reporting, and helpers for detecting
// LD_PRELOAD-based OTel injection and deriving agent types from paths.
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

// GetAgentInfo returns detailed information about the detected agent.
func (p *Process) GetAgentInfo() *AgentInfo {
	if !p.HasAgent {
		return &AgentInfo{Type: AgentNone}
	}

	agentType := AgentOther
	version := "unknown"
	isServerless := false

	if p.IsMiddlewareAgent {
		agentType = AgentMiddleware
		version = extractMWAgentVersion(p.AgentPath)
		isServerless = isMWServerlessAgent(p.AgentPath)
	} else {
		detectedType := detectJavaAgentType(p.AgentPath)
		if detectedType != AgentOther {
			agentType = detectedType
		}
	}

	return &AgentInfo{
		Type:         agentType,
		Path:         p.AgentPath,
		Name:         p.AgentName,
		Version:      version,
		IsServerless: isServerless,
	}
}

// FormatAgentStatus returns a human-readable agent status string.
func (p *Process) FormatAgentStatus() string {
	var status string

	if !p.HasAgent {
		status = "[x] None"
	} else {
		agentInfo := p.GetAgentInfo()
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

	if p.IsInContainer() {
		status += fmt.Sprintf(" (container: %s)", p.GetContainerRuntime())
	}

	return status
}

// HasInstrumentation checks if the process has any form of instrumentation.
func (p *Process) HasInstrumentation() bool {
	return p.HasAgent
}

// HasMiddlewareInstrumentation checks specifically for Middleware instrumentation.
func (p *Process) HasMiddlewareInstrumentation() bool {
	return p.IsMiddlewareAgent
}

// IsServerless checks if this appears to be a serverless deployment.
func (p *Process) IsServerless() bool {
	if !p.HasAgent {
		return false
	}
	return p.GetAgentInfo().IsServerless
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
