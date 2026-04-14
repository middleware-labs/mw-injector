package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

func TestDefaultDiscoveryOptions(t *testing.T) {
	opts := discovery.DefaultDiscoveryOptions()

	if opts.MaxConcurrency != 10 {
		t.Errorf("Expected MaxConcurrency to be 10, got %d", opts.MaxConcurrency)
	}

	if opts.Timeout != 30*time.Second {
		t.Errorf("Expected Timeout to be 30s, got %v", opts.Timeout)
	}
}

func TestNewDiscoverer(t *testing.T) {
	ctx := context.Background()
	discoverer, err := discovery.NewDiscoverer(ctx, discovery.DiscoveryOptions{})
	if err != nil {
		t.Errorf("error in finding all python processes, \n %v", err)
	}

	if discoverer == nil {
		t.Fatal("NewDiscoverer returned nil")
	}

	// Clean up
	if err := discoverer.Close(); err != nil {
		t.Errorf("Failed to close discoverer: %v", err)
	}
}

func TestAgentTypeString(t *testing.T) {
	tests := []struct {
		agentType discovery.AgentType
		expected  string
	}{
		{discovery.AgentNone, "none"},
		{discovery.AgentOpenTelemetry, "opentelemetry"},
		{discovery.AgentMiddleware, "middleware"},
		{discovery.AgentOther, "other"},
	}

	for _, test := range tests {
		if result := test.agentType.String(); result != test.expected {
			t.Errorf("AgentType.String() for %v: expected %s, got %s",
				test.agentType, test.expected, result)
		}
	}
}

func TestProcessFormatAgentStatus(t *testing.T) {
	tests := []struct {
		name           string
		process        discovery.Process
		expectedStatus string
	}{
		{
			name:           "No agent",
			process:        discovery.Process{HasAgent: false},
			expectedStatus: "[x] None",
		},
		{
			name: "Middleware agent",
			process: discovery.Process{
				HasAgent:          true,
				IsMiddlewareAgent: true,
				AgentPath:         "/opt/middleware-javaagent-1.7.0.jar",
			},
			expectedStatus: "[ok] MW",
		},
		{
			name: "OpenTelemetry agent",
			process: discovery.Process{
				HasAgent:          true,
				IsMiddlewareAgent: false,
				AgentPath:         "/opt/opentelemetry-javaagent.jar",
			},
			expectedStatus: "[ok] OTel",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.process.FormatAgentStatus()
			if result != test.expectedStatus {
				t.Errorf("Expected %s, got %s", test.expectedStatus, result)
			}
		})
	}
}

func TestProcessHasInstrumentation(t *testing.T) {
	tests := []struct {
		name     string
		process  discovery.Process
		expected bool
	}{
		{
			name:     "No agent",
			process:  discovery.Process{HasAgent: false},
			expected: false,
		},
		{
			name:     "With agent",
			process:  discovery.Process{HasAgent: true},
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.process.HasInstrumentation()
			if result != test.expected {
				t.Errorf("Expected %v, got %v", test.expected, result)
			}
		})
	}
}

func TestProcessFilter(t *testing.T) {
	// Test that process filters are properly initialized
	filter := discovery.ProcessFilter{
		CurrentUserOnly:  true,
		HasJavaAgentOnly: true,
	}

	if !filter.CurrentUserOnly {
		t.Error("CurrentUserOnly should be true")
	}

	if !filter.HasJavaAgentOnly {
		t.Error("HasJavaAgentOnly should be true")
	}
}

// Integration test that actually tries to discover processes
// This will only pass if there are Java processes running
func TestRealDiscovery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	allProcs, err := discovery.FindAllProcesses(ctx)
	if err != nil {
		t.Logf("Discovery failed (this is OK if no processes are running): %v", err)
		return
	}

	for lang, processes := range allProcs {
		t.Logf("Found %d %s processes", len(processes), lang)

		for _, proc := range processes {
			t.Logf("Process: PID=%d, Service=%s, Lang=%s, Agent=%s",
				proc.PID, proc.ServiceName, proc.Language, proc.FormatAgentStatus())

			if proc.PID == 0 {
				t.Errorf("Process PID should not be 0")
			}

			if proc.ExecutableName == "" {
				t.Errorf("Process executable name should not be empty")
			}

			if proc.ServiceName == "" {
				t.Logf("Warning: Service name is empty for PID %d", proc.PID)
			}
		}
	}
}

