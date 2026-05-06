// types.go defines core types shared across the discovery package: the Language
// enum used to classify processes, and the IntegrationInspector/IntegrationRegistry
// for future host-level integration detection (e.g. Redis, Kafka).
package discovery

// Language represents a programming language detected in a process.
type Language string

const (
	LangJava   Language = "java"
	LangNode   Language = "node"
	LangPython Language = "python"
	LangGo     Language = "go"
	LangRust   Language = "rust"
	LangRuby   Language = "ruby"
)

// --- Integration Detection (future use) ---

// IntegrationDetails holds metadata about a detected integration
// (e.g. Redis, Kafka) running on the host.
type IntegrationDetails struct {
	Type    string
	Version string
	Ports   map[string]int
	Config  map[string]string
}

// IntegrationInspector examines a ProcessInfo and returns integration
// details if the process matches.
type IntegrationInspector interface {
	Inspect(proc *ProcessInfo) (*IntegrationDetails, bool)
}

// IntegrationRegistry iterates integration inspectors, first match wins.
type IntegrationRegistry struct {
	inspectors []IntegrationInspector
}

// NewIntegrationRegistry returns an empty registry. Integration inspectors
// will be registered here when implemented.
func NewIntegrationRegistry() *IntegrationRegistry {
	return &IntegrationRegistry{}
}

// Detect runs every registered inspector against proc and returns the
// first matching integration.
func (r *IntegrationRegistry) Detect(proc *ProcessInfo) (*IntegrationDetails, bool) {
	for _, i := range r.inspectors {
		if details, ok := i.Inspect(proc); ok {
			return details, true
		}
	}
	return nil, false
}
