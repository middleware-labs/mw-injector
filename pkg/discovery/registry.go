// registry.go defines the LanguageHandler interface and HandlerRegistry that
// drive the language-agnostic discovery pipeline. Each supported language
// implements LanguageHandler; the registry selects the right handler during
// the classification phase (first match wins).
package discovery

// LanguageHandler encapsulates all language-specific behavior across the
// discovery pipeline. One implementation per supported language (Java, Node,
// Python), registered in a HandlerRegistry.
//
// Adding a new language means implementing this interface and registering
// the handler in NewHandlerRegistry — no other files need to change.
type LanguageHandler interface {
	// Lang returns the language identifier (LangJava, LangNode, LangPython).
	Lang() Language

	// Detect examines a raw /proc entry and returns true if this process
	// belongs to this language. Called during the classification phase;
	// the first handler that returns true wins.
	Detect(proc *ProcessInfo) bool

	// Enrich takes a classified ProcessInfo and populates a full Process
	// struct with language-specific details. Returns nil to skip the process
	// (e.g. ignored by cache, not a real application process).
	Enrich(info *ProcessInfo, opts DiscoveryOptions, detector *ContainerDetector) *Process

	// PassesFilter returns true if the enriched process should be included
	// in discovery results, based on the provided filter criteria.
	PassesFilter(proc *Process, filter ProcessFilter) bool

	// ToServiceSetting converts an enriched Process into a ServiceSetting
	// for reporting to the backend. Returns nil to exclude from the report
	// (e.g. Tomcat processes in Java are excluded).
	ToServiceSetting(proc *Process) *ServiceSetting
}

// HandlerRegistry holds all registered LanguageHandlers and provides lookup
// by language. Handlers are called in registration order during the
// classification phase — the first handler whose Detect returns true wins.
type HandlerRegistry struct {
	handlers []LanguageHandler
}

// NewHandlerRegistry returns a registry pre-loaded with handlers for all
// supported languages. Registration order matters: Java is checked first
// because its exe name ("java") is the most unambiguous.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: []LanguageHandler{
			&JavaHandler{},
			&NodeHandler{},
			&PythonHandler{},
		},
	}
}

// Handlers returns all registered handlers in registration order.
func (r *HandlerRegistry) Handlers() []LanguageHandler {
	return r.handlers
}

// ForLanguage returns the handler for the given language, or nil if not found.
func (r *HandlerRegistry) ForLanguage(lang Language) LanguageHandler {
	for _, h := range r.handlers {
		if h.Lang() == lang {
			return h
		}
	}
	return nil
}

// Detect runs every registered handler's Detect method against proc and
// returns the first matching language. This replaces the old LanguageRegistry.
func (r *HandlerRegistry) Detect(proc *ProcessInfo) (Language, bool) {
	for _, h := range r.handlers {
		if h.Detect(proc) {
			return h.Lang(), true
		}
	}
	return "", false
}
