package discovery

import "strings"

// JavaInspector detects Java processes by examining the executable name
// and command line. Adapted from mw-lang-detector pkg/inspectors/java/java.go
// combined with mw-injector's existing getJavaDiscoveryCandidateForProcesss logic.
type JavaInspector struct{}

func (ji *JavaInspector) Inspect(proc *ProcessInfo) (Language, bool) {
	exeLower := strings.ToLower(proc.ExeName)

	if exeLower == "java" || strings.HasSuffix(exeLower, "/java") {
		return LangJava, true
	}

	// Fallback: check cmdline for "java" as the first token (covers
	// cases where exe readlink resolved to a jdk path like /usr/lib/jvm/…/java).
	if strings.Contains(exeLower, "java") {
		return LangJava, true
	}

	cmdLower := strings.ToLower(proc.CmdLine)
	if strings.HasPrefix(cmdLower, "java ") || strings.Contains(cmdLower, "/java ") {
		return LangJava, true
	}

	return "", false
}
