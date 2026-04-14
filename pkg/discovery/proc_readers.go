// proc_readers.go reads process metadata from the /proc filesystem.
// These low-level helpers extract owner, status, creation time, parent PID,
// and cgroup information for a given PID. Used by language handlers during
// the enrichment phase.
package discovery

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// timeFromMillis converts a process creation time in milliseconds to a time.Time.
func timeFromMillis(millis int64) time.Time {
	return time.Unix(millis/1000, 0)
}

// fileBase is a shorthand for filepath.Base.
func fileBase(path string) string {
	return filepath.Base(path)
}

// readProcessOwner reads /proc/{pid}/status to find the UID, then resolves
// it to a username. Returns "unknown" if the PID is gone or the UID lookup fails.
func readProcessOwner(pid int32) string {
	path := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				u, err := user.LookupId(fields[1])
				if err != nil {
					return fields[1]
				}
				return u.Username
			}
		}
	}
	return "unknown"
}

// readProcessCreateTime reads /proc/{pid}/stat to extract the process start
// time in milliseconds since epoch. Returns 0 if the process is gone.
func readProcessCreateTime(pid int32) int64 {
	path := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	content := string(data)
	closeParen := strings.LastIndex(content, ")")
	if closeParen == -1 {
		return 0
	}

	fields := strings.Fields(content[closeParen+2:])
	if len(fields) < 20 {
		return 0
	}
	var startTime int64
	fmt.Sscanf(fields[19], "%d", &startTime)

	return startTime * (1000 / clockTickHz)
}

// readProcessStatus reads /proc/{pid}/status to extract the process state
// character (e.g. "S" for sleeping, "R" for running).
func readProcessStatus(pid int32) string {
	path := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "State:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return "unknown"
}

// readProcessPPID reads /proc/{pid}/status to extract the parent PID.
func readProcessPPID(pid int32) int32 {
	path := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				var ppid int32
				fmt.Sscanf(fields[1], "%d", &ppid)
				return ppid
			}
		}
	}
	return 0
}

// isIgnoredSystemdUnit checks whether the process belongs to a systemd unit
// that should be excluded from discovery (e.g. init.scope, systemd-journald).
func isIgnoredSystemdUnit(pid int32) bool {
	name, found := parseCgroupUnitName(pid)
	return found && ignoredSystemdUnits[name]
}
