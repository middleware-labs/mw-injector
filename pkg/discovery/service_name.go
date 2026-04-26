// service_name.go provides utilities for extracting and sanitizing service names
// from process metadata. These are shared across all language handlers during
// the enrichment phase to derive a human-readable OTEL service name.
package discovery

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	// cleanNameInvalid matches characters not allowed in sanitized service names.
	cleanNameInvalid = regexp.MustCompile(`[^a-z0-9\-]+`)
	// cleanNameMultiDash collapses consecutive dashes into one.
	cleanNameMultiDash = regexp.MustCompile(`-+`)
)

// cleanName sanitizes a raw name into a lowercase, dash-separated identifier
// suitable for use as an OTEL service name. Returns "" for empty or overly
// generic names (e.g. "java", "app", "server").
func cleanName(name string) string {
	if name == "" {
		return ""
	}
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "_", "-")
	name = cleanNameInvalid.ReplaceAllString(name, "")
	name = strings.Trim(name, "-")
	name = cleanNameMultiDash.ReplaceAllString(name, "-")

	genericServiceNames := map[string]bool{
		"java": true, "app": true, "application": true, "service": true,
		"server": true, "main": true, "demo": true, "test": true,
		"example": true, "sample": true, "hello": true, "world": true,
	}
	if name == "" || genericServiceNames[name] {
		return ""
	}
	return name
}

// extractServiceNameFromEnviron reads /proc/{pid}/environ looking for
// well-known service name environment variables (OTEL_SERVICE_NAME,
// SERVICE_NAME, FLASK_APP). Returns "" if none found.
func extractServiceNameFromEnviron(pid int32) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return ""
	}

	envVars := strings.Split(string(data), "\x00")
	for _, env := range envVars {
		if strings.HasPrefix(env, "OTEL_SERVICE_NAME=") ||
			strings.HasPrefix(env, "SERVICE_NAME=") ||
			strings.HasPrefix(env, "FLASK_APP=") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) > 1 && parts[1] != "" {
				return parts[1]
			}
		}
	}
	return ""
}

// serviceNameFromWorkDir derives a service name from a working directory path
// by taking the last two meaningful (non-generic) segments and joining them
// with a dash. This gives better context than a single segment — e.g.
// "/home/user/browse-bay/backend" → "browse-bay-backend" rather than just
// "backend".
func serviceNameFromWorkDir(dir string) string {
	if dir == "" {
		return ""
	}

	parts := strings.Split(dir, "/")

	genericDirs := map[string]bool{
		"": true, ".": true, "..": true, "/": true, "home": true, "opt": true,
		"usr": true, "var": true, "tmp": true, "app": true, "apps": true,
		"bin": true, "lib": true, "lib64": true, "src": true, "main": true,
		"resources": true, "static": true, "public": true, "build": true,
		"dist": true, "node-modules": true, "work": true,
	}

	var meaningful []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && !genericDirs[strings.ToLower(part)] {
			meaningful = append(meaningful, part)
		}
	}

	if len(meaningful) == 0 {
		return ""
	}

	// Take the last 2 meaningful segments for context.
	start := len(meaningful) - 2
	if start < 0 {
		start = 0
	}
	name := strings.Join(meaningful[start:], "-")
	return cleanName(name)
}

// extractSystemdUnit parses the process cgroup to find its systemd unit name.
// Returns "" if the process is not managed by systemd or belongs to an ignored unit.
func extractSystemdUnit(pid int32) string {
	name, found := parseCgroupUnitName(pid)
	if !found || ignoredSystemdUnits[name] {
		return ""
	}
	return name
}
