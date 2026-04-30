package discovery

import (
	"os"
	"testing"
)

func TestScanProcesses_ReturnsSelf(t *testing.T) {
	procs := ScanProcesses()
	if len(procs) == 0 {
		t.Fatal("ScanProcesses returned no processes; expected at least the test process itself")
	}

	selfPID := int32(os.Getpid())
	found := false
	for _, p := range procs {
		if p.PID == selfPID {
			found = true
			if p.ExeName == "" {
				t.Error("ExeName is empty for the test process")
			}
			if p.ExePath == "" {
				t.Error("ExePath is empty for the test process")
			}
			if p.CmdLine == "" {
				t.Error("CmdLine is empty for the test process")
			}
			if len(p.CmdArgs) == 0 {
				t.Error("CmdArgs is empty for the test process")
			}
			break
		}
	}

	if !found {
		t.Errorf("ScanProcesses did not find the test process itself (PID %d)", selfPID)
	}
}

func TestScanProcesses_AllHaveBasicFields(t *testing.T) {
	procs := ScanProcesses()

	for _, p := range procs {
		if p.PID <= 0 {
			t.Errorf("invalid PID: %d", p.PID)
		}
		if p.ExeName == "" {
			t.Errorf("PID %d: ExeName is empty", p.PID)
		}
		if p.CmdLine == "" {
			t.Errorf("PID %d: CmdLine is empty", p.PID)
		}
	}
}

func TestReadProcDetails_InvalidPID(t *testing.T) {
	_, ok := readProcDetails(-1)
	if ok {
		t.Error("readProcDetails should return false for invalid PID -1")
	}

	_, ok = readProcDetails(999999999)
	if ok {
		t.Error("readProcDetails should return false for non-existent PID 999999999")
	}
}

func TestReadProcDetails_Self(t *testing.T) {
	selfPID := int32(os.Getpid())

	info, ok := readProcDetails(selfPID)
	if !ok {
		t.Fatalf("readProcDetails should succeed for the test process PID %d", selfPID)
	}

	if info.PID != selfPID {
		t.Errorf("expected PID %d, got %d", selfPID, info.PID)
	}
	if info.ExePath == "" {
		t.Error("ExePath should not be empty for self")
	}
	if len(info.CmdArgs) == 0 {
		t.Error("CmdArgs should not be empty for self")
	}
}

func TestReadSelectedEnviron_Self(t *testing.T) {
	selfPID := int32(os.Getpid())
	env := readSelectedEnviron(selfPID)

	// /proc/<pid>/environ is populated at process start and only contains
	// variables from relevantEnvPrefixes. In CI/test contexts we may not
	// have any of those set, so nil is a valid result.
	if env != nil {
		for key := range env {
			found := false
			for _, prefix := range relevantEnvPrefixes {
				trimmed := key
				if trimmed+"=" == prefix || trimmed == key {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("unexpected env key %q in selected environ", key)
			}
		}
	}
}

func TestParseCmdline(t *testing.T) {
	tests := []struct {
		name     string
		raw      string // cmdline bytes (use \x00 for null separators)
		exeName  string
		wantArgs []string
		wantCmd  string
	}{
		{
			name:     "normal null-separated args",
			raw:      "node\x00server.js\x00",
			exeName:  "node",
			wantArgs: []string{"node", "server.js"},
			wantCmd:  "node server.js",
		},
		{
			name:     "pm2 rewritten argv with trailing nulls",
			raw:      "node /home/user/app/index.js\x00\x00\x00\x00",
			exeName:  "node",
			wantArgs: []string{"node", "/home/user/app/index.js"},
			wantCmd:  "node /home/user/app/index.js",
		},
		{
			name:     "pm2 god daemon does not match exe",
			raw:      "PM2 v6.0.14: God Daemon (/home/user/.pm2)\x00\x00",
			exeName:  "node",
			wantArgs: []string{"PM2 v6.0.14: God Daemon (/home/user/.pm2)"},
			wantCmd:  "PM2 v6.0.14: God Daemon (/home/user/.pm2)",
		},
		{
			name:     "npm space-joined",
			raw:      "npm start\x00\x00\x00",
			exeName:  "node",
			wantArgs: []string{"npm start"},
			wantCmd:  "npm start",
		},
		{
			name:     "single arg no spaces",
			raw:      "node\x00",
			exeName:  "node",
			wantArgs: []string{"node"},
			wantCmd:  "node",
		},
		{
			name:     "java rewritten argv",
			raw:      "java -jar /opt/app.jar\x00\x00",
			exeName:  "java",
			wantArgs: []string{"java", "-jar", "/opt/app.jar"},
			wantCmd:  "java -jar /opt/app.jar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, cmd := parseCmdline([]byte(tt.raw), tt.exeName)
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("args length = %d, want %d\n  got:  %v\n  want: %v", len(args), len(tt.wantArgs), args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
			if cmd != tt.wantCmd {
				t.Errorf("cmdline = %q, want %q", cmd, tt.wantCmd)
			}
		})
	}
}

func TestReadSelectedEnviron_Filters(t *testing.T) {
	// HOME should NOT be in the selected environ (not in relevantEnvPrefixes)
	selfPID := int32(os.Getpid())
	env := readSelectedEnviron(selfPID)

	if _, ok := env["HOME"]; ok {
		t.Error("HOME should not be included in selected environ")
	}
	if _, ok := env["PATH"]; ok {
		t.Error("PATH should not be included in selected environ")
	}
}
