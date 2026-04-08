package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcessInfo holds the raw data gathered from a single /proc/<pid> entry.
// It is the universal input to all language and integration inspectors.
type ProcessInfo struct {
	PID     int32
	ExeName string            // basename of /proc/<pid>/exe readlink
	ExePath string            // full resolved path from /proc/<pid>/exe readlink
	CmdLine string            // raw /proc/<pid>/cmdline (null bytes replaced with spaces)
	CmdArgs []string          // cmdline split into individual arguments
	Environ map[string]string // selected environment variables from /proc/<pid>/environ
}

// ScanProcesses reads /proc once and returns a ProcessInfo for every numeric
// PID directory. Errors on individual PIDs (e.g. permission denied, process
// gone) are silently skipped — the caller gets as many results as possible.
//
// This replaces the gopsutil process.Processes() call with a direct /proc
// read, adapted from mw-lang-detector's optimize-code branch pattern.
func ScanProcesses() []ProcessInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	results := make([]ProcessInfo, 0, len(entries)/2)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.ParseInt(entry.Name(), 10, 32)
		if err != nil {
			continue // not a numeric PID directory
		}

		info, ok := readProcDetails(int32(pid))
		if !ok {
			continue
		}

		results = append(results, info)
	}

	return results
}

// readProcDetails gathers exe, cmdline, and selected environ for a single PID.
// Returns false if the process can't be read (gone, permission denied, etc.).
func readProcDetails(pid int32) (ProcessInfo, bool) {
	base := fmt.Sprintf("/proc/%d", pid)

	exePath, err := os.Readlink(filepath.Join(base, "exe"))
	if err != nil {
		return ProcessInfo{}, false
	}

	cmdlineRaw, err := os.ReadFile(filepath.Join(base, "cmdline"))
	if err != nil || len(cmdlineRaw) == 0 {
		return ProcessInfo{}, false
	}

	// /proc/<pid>/cmdline uses null bytes as separators.
	// Build both a space-joined string and an arg slice.
	var args []string
	for _, seg := range strings.Split(string(cmdlineRaw), "\x00") {
		if seg != "" {
			args = append(args, seg)
		}
	}
	cmdline := strings.Join(args, " ")

	environ := readSelectedEnviron(pid)

	return ProcessInfo{
		PID:     pid,
		ExeName: filepath.Base(exePath),
		ExePath: exePath,
		CmdLine: cmdline,
		CmdArgs: args,
		Environ: environ,
	}, true
}

// relevantEnvPrefixes is the set of environment variable prefixes that
// are worth reading. Keeping this small avoids copying megabytes of
// environment data per process.
var relevantEnvPrefixes = []string{
	"OTEL_SERVICE_NAME=",
	"SERVICE_NAME=",
	"FLASK_APP=",
	"NODE_OPTIONS=",
	"JAVA_TOOL_OPTIONS=",
	"CATALINA_OPTS=",
	"JAVA_OPTS=",
	"LD_PRELOAD=",
	"PYTHONPATH=",
	"PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX=",
}

// readSelectedEnviron reads /proc/<pid>/environ and returns only the
// environment variables whose keys match relevantEnvPrefixes.
func readSelectedEnviron(pid int32) map[string]string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return nil
	}

	result := make(map[string]string)
	for _, entry := range strings.Split(string(data), "\x00") {
		if entry == "" {
			continue
		}
		for _, prefix := range relevantEnvPrefixes {
			key := strings.TrimSuffix(prefix, "=")
			if strings.HasPrefix(entry, prefix) {
				result[key] = strings.TrimPrefix(entry, prefix)
				break
			}
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}
