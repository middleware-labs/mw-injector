package discovery

// Language represents a programming language detected in a process.
type Language string

const (
	LangJava   Language = "java"
	LangNode   Language = "node"
	LangPython Language = "python"
)

// --- Language Detection ---

// LanguageInspector examines a ProcessInfo and returns the detected language.
// Implementations must be safe for concurrent use.
type LanguageInspector interface {
	Inspect(proc *ProcessInfo) (Language, bool)
}

// LanguageRegistry iterates a list of inspectors in registration order.
// The first match wins — this mirrors mw-lang-detector's DetectLanguage pattern.
type LanguageRegistry struct {
	inspectors []LanguageInspector
}

// NewLanguageRegistry returns a registry pre-loaded with the built-in
// language inspectors. Order matters: Java is checked first because its
// exe name ("java") is the most unambiguous.
func NewLanguageRegistry() *LanguageRegistry {
	return &LanguageRegistry{
		inspectors: []LanguageInspector{
			&JavaInspector{},
			&NodeInspector{},
			&PythonInspector{},
		},
	}
}

// Detect runs every registered inspector against proc and returns the
// first matching language.
func (r *LanguageRegistry) Detect(proc *ProcessInfo) (Language, bool) {
	for _, i := range r.inspectors {
		if lang, ok := i.Inspect(proc); ok {
			return lang, true
		}
	}
	return "", false
}

// --- Integration Detection (future use) ---

// IntegrationDetails holds metadata about a detected integration
// (e.g. Redis, Kafka) running on the host. Defined now so that
// integration inspectors can plug in later without changing the interface.
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
