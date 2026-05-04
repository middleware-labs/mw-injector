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
