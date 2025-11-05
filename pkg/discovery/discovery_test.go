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

	if !opts.SkipPermissionErrors {
		t.Errorf("Expected SkipPermissionErrors to be true")
	}

	if !opts.IncludeMetrics {
		t.Errorf("Expected IncludeMetrics to be true")
	}
}

func TestNewDiscoverer(t *testing.T) {
	ctx := context.Background()
	discoverer := discovery.NewDiscoverer(ctx)

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

func TestJavaProcessFormatAgentStatus(t *testing.T) {
	tests := []struct {
		name           string
		process        discovery.JavaProcess
		expectedStatus string
	}{
		{
			name:           "No agent",
			process:        discovery.JavaProcess{HasJavaAgent: false},
			expectedStatus: "[x] None",
		},
		{
			name: "Middleware agent",
			process: discovery.JavaProcess{
				HasJavaAgent:      true,
				IsMiddlewareAgent: true,
				JavaAgentPath:     "/opt/middleware-javaagent-1.7.0.jar",
			},
			expectedStatus: "✅ MW",
		},
		{
			name: "OpenTelemetry agent",
			process: discovery.JavaProcess{
				HasJavaAgent:      true,
				IsMiddlewareAgent: false,
				JavaAgentPath:     "/opt/opentelemetry-javaagent.jar",
			},
			expectedStatus: "✅ OTel",
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

func TestJavaProcessHasInstrumentation(t *testing.T) {
	tests := []struct {
		name     string
		process  discovery.JavaProcess
		expected bool
	}{
		{
			name:     "No agent",
			process:  discovery.JavaProcess{HasJavaAgent: false},
			expected: false,
		},
		{
			name:     "With agent",
			process:  discovery.JavaProcess{HasJavaAgent: true},
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

	processes, err := discovery.FindAllJavaProcesses(ctx)
	if err != nil {
		t.Logf("Discovery failed (this is OK if no Java processes are running): %v", err)
		return
	}

	t.Logf("Found %d Java processes", len(processes))

	for _, proc := range processes {
		t.Logf("Process: PID=%d, Service=%s, JAR=%s, Agent=%s",
			proc.ProcessPID, proc.ServiceName, proc.JarFile, proc.FormatAgentStatus())

		// Validate required fields
		if proc.ProcessPID == 0 {
			t.Errorf("Process PID should not be 0")
		}

		if proc.ProcessExecutableName == "" {
			t.Errorf("Process executable name should not be empty")
		}

		if proc.ServiceName == "" {
			t.Logf("Warning: Service name is empty for PID %d", proc.ProcessPID)
		}
	}
}

// Test discovery with filters
func TestDiscoveryWithFilters(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test current user filter
	processes, err := discovery.FindCurrentUserJavaProcesses(ctx)
	if err != nil {
		t.Logf("Current user discovery failed: %v", err)
		return
	}

	t.Logf("Found %d Java processes for current user", len(processes))

	// Test instrumented processes filter
	instrumented, err := discovery.FindInstrumentedProcesses(ctx)
	if err != nil {
		t.Logf("Instrumented process discovery failed: %v", err)
		return
	}

	t.Logf("Found %d instrumented Java processes", len(instrumented))

	// Validate that all returned processes actually have instrumentation
	for _, proc := range instrumented {
		if !proc.HasInstrumentation() {
			t.Errorf("Process PID %d should have instrumentation but doesn't", proc.ProcessPID)
		}
	}
}
