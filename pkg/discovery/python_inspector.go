package discovery

import "strings"

// PythonInspector detects Python processes by examining the executable name
// and command line. Adapted from mw-lang-detector pkg/inspectors/python/python.go
// combined with mw-injector's existing isPythonProcess logic.
type PythonInspector struct{}

var pythonExecutables = map[string]bool{
	"python":  true,
	"python2": true,
	"python3": true,
	"pypy":    true,
	"pypy3":   true,
}

// Python-based entry-point binaries that are not named "python*"
var pythonBinaries = map[string]bool{
	"gunicorn":     true,
	"uvicorn":      true,
	"celery":       true,
	"flask":        true,
	"django-admin": true,
}

var pythonCmdPatterns = []string{
	"python ",
	"python3 ",
	"gunicorn ",
	"uvicorn ",
	"celery ",
	"manage.py runserver",
	"flask run",
}

func (pi *PythonInspector) Inspect(proc *ProcessInfo) (Language, bool) {
	exeLower := strings.ToLower(proc.ExeName)

	// Check standard python names (python, python3, python3.10, etc.)
	if pythonExecutables[exeLower] || strings.HasPrefix(exeLower, "python3.") {
		return LangPython, true
	}

	if pythonBinaries[exeLower] {
		return LangPython, true
	}

	cmdLower := strings.ToLower(proc.CmdLine)
	for _, pattern := range pythonCmdPatterns {
		if strings.Contains(cmdLower, pattern) {
			return LangPython, true
		}
	}

	// Fallback: any .py file reference in cmdline
	if strings.Contains(cmdLower, ".py") {
		return LangPython, true
	}

	return "", false
}
