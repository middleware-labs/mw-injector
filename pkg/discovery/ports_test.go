package discovery

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
)

// TestDiscoveryLoggerEmitsTimings confirms that when a slog logger is
// passed via DiscoveryOptions, discovery emits structured timing records.
// Nil-logger callers should see no output and no panic.
func TestDiscoveryLoggerEmitsTimings(t *testing.T) {
	// Nil-logger path: must not panic.
	_, err := FindAllProcesses(context.Background())
	if err != nil {
		t.Fatalf("nil-logger path errored: %v", err)
	}

	// Real logger: capture output.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err = FindAllProcessesWithLogger(context.Background(), logger)
	if err != nil {
		t.Fatalf("logger path errored: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"classification complete", "discovery complete"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected log output to contain %q, got: %s", want, out)
		}
	}
}

func TestParseHexAddrPort(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		isV6     bool
		wantAddr string
		wantPort uint16
		wantOK   bool
	}{
		{
			name:     "ipv4 localhost:8080",
			input:    "0100007F:1F90",
			isV6:     false,
			wantAddr: "127.0.0.1",
			wantPort: 8080,
			wantOK:   true,
		},
		{
			name:     "ipv4 wildcard:80",
			input:    "00000000:0050",
			isV6:     false,
			wantAddr: "0.0.0.0",
			wantPort: 80,
			wantOK:   true,
		},
		{
			name:     "ipv6 wildcard:8080",
			input:    "00000000000000000000000000000000:1F90",
			isV6:     true,
			wantAddr: "0:0:0:0:0:0:0:0",
			wantPort: 8080,
			wantOK:   true,
		},
		{
			name:   "malformed no colon",
			input:  "0100007F1F90",
			isV6:   false,
			wantOK: false,
		},
		{
			name:   "ipv4 wrong length",
			input:  "01007F:1F90",
			isV6:   false,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr, port, ok := parseHexAddrPort(tc.input, tc.isV6)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if addr != tc.wantAddr {
				t.Errorf("addr = %q, want %q", addr, tc.wantAddr)
			}
			if port != tc.wantPort {
				t.Errorf("port = %d, want %d", port, tc.wantPort)
			}
		})
	}
}

// TestAttachListenersDedup binds a TCP port in the test process and
// verifies that AttachListeners populates DetailListeners for a Process
// pointing at the current PID. Multiple Process entries for the same PID
// should share the same netns and each get the listener attached.
func TestAttachListenersDedup(t *testing.T) {
	if _, err := os.Stat("/proc/self/net/tcp"); err != nil {
		t.Skip("/proc/self/net/tcp not available")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	wantPort := uint16(ln.Addr().(*net.TCPAddr).Port)

	// Two Process entries with the same PID exercise the netns grouping
	// path — they should share a single /proc/net/tcp read.
	procs := []*Process{
		{PID: int32(os.Getpid())},
		{PID: int32(os.Getpid())},
	}
	AttachListeners(procs)

	for i, p := range procs {
		ls := p.Listeners()
		if len(ls) == 0 {
			t.Fatalf("procs[%d]: no listeners attached", i)
		}
		found := false
		for _, l := range ls {
			if (l.Protocol == "tcp" || l.Protocol == "tcp6") && l.Port == wantPort {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("procs[%d]: bound port %d not found in %+v", i, wantPort, ls)
		}
	}
}

func TestSplitCmdline(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want []string
	}{
		{
			name: "null-separated args",
			data: []byte("node\x00/usr/bin/pm2-runtime\x00app.js\x00-i\x00max\x00"),
			want: []string{"node", "/usr/bin/pm2-runtime", "app.js", "-i", "max"},
		},
		{
			name: "god daemon rewritten argv",
			data: []byte("PM2 v6.0.14: God Daemon (/home/user/.pm2)\x00"),
			want: []string{"PM2 v6.0.14: God Daemon (/home/user/.pm2)"},
		},
		{
			name: "empty",
			data: []byte{},
			want: nil,
		},
		{
			name: "single null",
			data: []byte("\x00"),
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitCmdline(tc.data)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d; got %v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestInheritParentPorts(t *testing.T) {
	// Helpers to build minimal Process values with a stable fingerprint.
	// Fingerprint uses language + entry_point + working_directory + package_name + container_name.
	makeWorker := func(pid, ppid int32, entryPoint string) *Process {
		return &Process{
			PID:       pid,
			ParentPID: ppid,
			Language:  LangNode,
			Details: map[string]any{
				DetailEntryPoint:       entryPoint,
				DetailWorkingDirectory: "/app",
			},
		}
	}

	daemonPorts := []Listener{
		{Protocol: "tcp6", Address: "::", Port: 3000},
		{Protocol: "tcp6", Address: "::", Port: 3001},
	}
	alwaysPM2 := func(int32) bool { return true }
	neverPM2 := func(int32) bool { return false }
	returnPorts := func(int32) []Listener { return daemonPorts }
	returnNoPorts := func(int32) []Listener { return nil }

	t.Run("single-app daemon inherits ports to workers", func(t *testing.T) {
		w1 := makeWorker(200, 100, "index.js")
		w2 := makeWorker(201, 100, "index.js")
		procs := []*Process{w1, w2}

		inheritParentPortsWith(procs, alwaysPM2, returnPorts)

		for i, w := range []*Process{w1, w2} {
			ls := w.Listeners()
			if len(ls) != 2 {
				t.Errorf("worker %d: got %d listeners, want 2", i, len(ls))
			}
		}
	})

	t.Run("multi-app daemon does not inherit (different fingerprints)", func(t *testing.T) {
		w1 := makeWorker(200, 100, "api.js")
		w2 := makeWorker(201, 100, "worker.js") // different entry point → different fingerprint
		procs := []*Process{w1, w2}

		inheritParentPortsWith(procs, alwaysPM2, returnPorts)

		for i, w := range []*Process{w1, w2} {
			if ls := w.Listeners(); len(ls) != 0 {
				t.Errorf("worker %d: got %d listeners, want 0 (multi-app safety)", i, len(ls))
			}
		}
	})

	t.Run("non-PM2 parent does not inherit", func(t *testing.T) {
		w1 := makeWorker(200, 100, "index.js")
		procs := []*Process{w1}

		inheritParentPortsWith(procs, neverPM2, returnPorts)

		if ls := w1.Listeners(); len(ls) != 0 {
			t.Errorf("got %d listeners, want 0 (non-PM2 parent)", len(ls))
		}
	})

	t.Run("workers that already have listeners are skipped", func(t *testing.T) {
		w1 := makeWorker(200, 100, "index.js")
		w1.Details[DetailListeners] = []Listener{{Protocol: "tcp", Port: 9999}}
		w2 := makeWorker(201, 100, "index.js")
		procs := []*Process{w1, w2}

		inheritParentPortsWith(procs, alwaysPM2, returnPorts)

		// w1 should keep its original port, not get daemon ports
		ls1 := w1.Listeners()
		if len(ls1) != 1 || ls1[0].Port != 9999 {
			t.Errorf("w1: got %+v, want [{tcp 9999}] (original preserved)", ls1)
		}
		// w2 is the only candidate — but now it's the sole worker with one
		// fingerprint, so it should get daemon ports
		ls2 := w2.Listeners()
		if len(ls2) != 2 {
			t.Errorf("w2: got %d listeners, want 2", len(ls2))
		}
	})

	t.Run("non-Node processes are ignored", func(t *testing.T) {
		javaProc := &Process{
			PID:       200,
			ParentPID: 100,
			Language:  LangJava,
			Details:   make(map[string]any),
		}
		procs := []*Process{javaProc}

		inheritParentPortsWith(procs, alwaysPM2, returnPorts)

		if ls := javaProc.Listeners(); len(ls) != 0 {
			t.Errorf("got %d listeners, want 0 (non-Node)", len(ls))
		}
	})

	t.Run("parent with no listeners is a no-op", func(t *testing.T) {
		w1 := makeWorker(200, 100, "index.js")
		procs := []*Process{w1}

		inheritParentPortsWith(procs, alwaysPM2, returnNoPorts)

		if ls := w1.Listeners(); len(ls) != 0 {
			t.Errorf("got %d listeners, want 0 (parent has no ports)", len(ls))
		}
	})

	t.Run("parent PID <= 1 is skipped", func(t *testing.T) {
		w1 := makeWorker(200, 1, "index.js")
		procs := []*Process{w1}

		inheritParentPortsWith(procs, alwaysPM2, returnPorts)

		if ls := w1.Listeners(); len(ls) != 0 {
			t.Errorf("got %d listeners, want 0 (parent PID 1)", len(ls))
		}
	})
}

func TestIsPM2CmdLine(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "god daemon rewritten argv",
			args: []string{"PM2 v6.0.14: God Daemon (/home/user/.pm2)"},
			want: true,
		},
		{
			name: "pm2 start null-separated",
			args: []string{"pm2", "start", "app.js"},
			want: true,
		},
		{
			name: "node running pm2-runtime",
			args: []string{"node", "/usr/local/bin/pm2-runtime", "ecosystem.config.js"},
			want: true,
		},
		{
			name: "node running pm2-dev",
			args: []string{"node", "/usr/local/lib/node_modules/pm2/bin/pm2-dev", "app.js"},
			want: true,
		},
		{
			name: "node running pm2 binary",
			args: []string{"node", "/usr/local/bin/pm2", "start", "app.js"},
			want: true,
		},
		{
			name: "node with flags before pm2-runtime",
			args: []string{"node", "--max-old-space-size=4096", "/usr/local/bin/pm2-runtime", "ecosystem.config.js"},
			want: true,
		},
		{
			name: "regular node app",
			args: []string{"node", "/app/index.js"},
			want: false,
		},
		{
			name: "regular node app with flags",
			args: []string{"node", "--inspect", "/app/server.js"},
			want: false,
		},
		{
			name: "node with only flags",
			args: []string{"node", "--inspect"},
			want: false,
		},
		{
			name: "empty args",
			args: nil,
			want: false,
		},
		{
			name: "python process",
			args: []string{"python3", "app.py"},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPM2CmdLine(tc.args); got != tc.want {
				t.Errorf("isPM2CmdLine(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// TestListListenersSelf binds a TCP port in the test process and verifies
// ListListeners reports it. Skips if /proc isn't available.
func TestListListenersSelf(t *testing.T) {
	if _, err := os.Stat("/proc/self/net/tcp"); err != nil {
		t.Skip("/proc/self/net/tcp not available")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	wantPort := uint16(ln.Addr().(*net.TCPAddr).Port)
	listeners := ListListeners(int32(os.Getpid()))

	for _, l := range listeners {
		if (l.Protocol == "tcp" || l.Protocol == "tcp6") && l.Port == wantPort {
			return
		}
	}
	t.Fatalf("bound port %d not found in %+v", wantPort, listeners)
}
