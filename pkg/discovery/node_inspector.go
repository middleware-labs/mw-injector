package discovery

import "strings"

// NodeInspector detects Node.js processes by examining the executable name
// and command line. Adapted from mw-lang-detector pkg/inspectors/nodejs/nodejs.go
// combined with mw-injector's existing isNodeProcess logic.
type NodeInspector struct{}

var nodeExecutables = map[string]bool{
	"node":   true,
	"nodejs": true,
}

var nodeCmdPatterns = []string{
	"node ",
	"npm start",
	"npm run",
	"npx ",
	"yarn start",
	"yarn run",
}

func (ni *NodeInspector) Inspect(proc *ProcessInfo) (Language, bool) {
	exeLower := strings.ToLower(proc.ExeName)
	if nodeExecutables[exeLower] {
		return LangNode, true
	}

	cmdLower := strings.ToLower(proc.CmdLine)
	for _, pattern := range nodeCmdPatterns {
		if strings.Contains(cmdLower, pattern) {
			return LangNode, true
		}
	}

	return "", false
}
