// ports.go detects listening TCP/UDP ports for processes by correlating
// socket inodes in /proc/<pid>/fd/* with entries in /proc/<pid>/net/*.
//
// Design note (batch + netns dedup):
//
// The network tables at /proc/<pid>/net/{tcp,tcp6,udp,udp6} belong to the
// process's *network namespace*, not the process itself — every PID in the
// same netns sees identical bytes. AttachListeners exploits this by grouping
// target PIDs by their netns inode (readlink /proc/<pid>/ns/net) and reading
// the four net tables once per unique netns. On a typical host with one
// netns, this is 4 reads total regardless of how many processes we inspect,
// versus 4N for a naive per-PID loop.
//
// The /proc/<pid>/fd walk is still per-PID (socket inodes are per-process),
// but that's unavoidable — it's how we know which sockets the PID owns.
//
// We deliberately stick with the text /proc interface rather than netlink
// (sock_diag / inet_diag). Netlink is 2-3x faster on hosts with 100k+
// sockets but brings setns(), more code, and no measurable benefit at the
// scale mw-injector operates (10-100 classified processes per host).
package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Listener describes a single listening socket owned by a process.
type Listener struct {
	Protocol string // "tcp", "tcp6", "udp", "udp6"
	Address  string // local bind address (e.g. "0.0.0.0", "::", "127.0.0.1")
	Port     uint16
}

// tcpStateListen is the /proc/net/tcp hex state value for LISTEN.
const tcpStateListen = "0A"

// AttachListeners populates the DetailListeners key on each process whose
// PID has listening sockets. Processes are grouped by network namespace
// so /proc/<pid>/net/* is read once per unique netns instead of once per
// PID. Processes with no listeners are left untouched.
func AttachListeners(procs []*Process) {
	if len(procs) == 0 {
		return
	}

	// Group processes by netns inode. The map value is one representative
	// PID per netns (used to read the shared /proc/<pid>/net/* tables) and
	// the list of all processes in that netns (used to match fd inodes).
	type nsGroup struct {
		repPID  int32
		members []*Process
	}
	groups := make(map[uint64]*nsGroup)
	unknownNS := []*Process{} // processes whose netns we couldn't determine

	for _, p := range procs {
		ino, ok := readNetnsInode(p.PID)
		if !ok {
			unknownNS = append(unknownNS, p)
			continue
		}
		g, exists := groups[ino]
		if !exists {
			g = &nsGroup{repPID: p.PID}
			groups[ino] = g
		}
		g.members = append(g.members, p)
	}

	// For each netns, read the four net tables once via the representative
	// PID, then for each member PID walk its fds and match inodes.
	for _, g := range groups {
		byInode := buildInodeIndex(g.repPID)
		if len(byInode) == 0 {
			continue
		}
		for _, p := range g.members {
			attachFromIndex(p, byInode)
		}
	}

	// Fallback for PIDs whose netns we couldn't read (process gone,
	// permission denied): read their net tables per-PID.
	for _, p := range unknownNS {
		byInode := buildInodeIndex(p.PID)
		if len(byInode) == 0 {
			continue
		}
		attachFromIndex(p, byInode)
	}
}

// ListListeners returns every listening TCP socket and bound UDP socket
// owned by the given PID. Convenience wrapper around AttachListeners for
// callers that have only a PID and not a Process. Prefer AttachListeners
// when you have multiple PIDs — it deduplicates net-table reads by netns.
func ListListeners(pid int32) []Listener {
	byInode := buildInodeIndex(pid)
	if len(byInode) == 0 {
		return nil
	}
	return matchFDs(pid, byInode)
}

// readNetnsInode returns the netns inode for a PID via readlink on
// /proc/<pid>/ns/net. The link target has the form "net:[4026531833]".
func readNetnsInode(pid int32) (uint64, bool) {
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/net", pid))
	if err != nil {
		return 0, false
	}
	const prefix = "net:["
	if !strings.HasPrefix(target, prefix) || !strings.HasSuffix(target, "]") {
		return 0, false
	}
	raw := target[len(prefix) : len(target)-1]
	ino, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return ino, true
}

// buildInodeIndex reads all four /proc/<pid>/net/* tables and returns a
// map from socket inode to Listener. Only rows that represent a listening
// socket (TCP LISTEN state, or any UDP bind) are kept.
//
// The caller can pass any PID in the target netns — the kernel returns
// the same bytes for every PID in that namespace.
func buildInodeIndex(pid int32) map[uint64]Listener {
	index := make(map[uint64]Listener)
	for _, proto := range []string{"tcp", "tcp6", "udp", "udp6"} {
		for _, e := range parseProcNet(pid, proto) {
			if (proto == "tcp" || proto == "tcp6") && e.state != tcpStateListen {
				continue
			}
			index[e.inode] = Listener{
				Protocol: proto,
				Address:  e.addr,
				Port:     e.port,
			}
		}
	}
	return index
}

// attachFromIndex walks /proc/<pid>/fd, matches socket inodes against the
// netns-level inode index, and stores matching listeners on the Process.
func attachFromIndex(p *Process, byInode map[uint64]Listener) {
	listeners := matchFDs(p.PID, byInode)
	if len(listeners) == 0 {
		return
	}
	if p.Details == nil {
		p.Details = make(map[string]any)
	}
	p.Details[DetailListeners] = listeners
}

// matchFDs returns the listeners whose inodes appear in the PID's fd table.
func matchFDs(pid int32, byInode map[uint64]Listener) []Listener {
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return nil
	}

	var out []Listener
	seen := make(map[uint64]bool)
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err != nil {
			continue
		}
		if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
			continue
		}
		raw := target[len("socket:[") : len(target)-1]
		ino, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			continue
		}
		if seen[ino] {
			continue
		}
		if l, ok := byInode[ino]; ok {
			out = append(out, l)
			seen[ino] = true
		}
	}
	return out
}

// procNetEntry is a parsed row from /proc/<pid>/net/{tcp,tcp6,udp,udp6}.
type procNetEntry struct {
	addr  string
	port  uint16
	state string
	inode uint64
}

// parseProcNet reads /proc/<pid>/net/<proto> and returns each row's
// local address, port, state, and socket inode.
//
// Line format (header first, then one entry per line):
//
//	sl  local_address rem_address   st tx_queue:rx_queue tr:tm->when retrnsmt uid timeout inode ...
//	 0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000  0 12345 ...
func parseProcNet(pid int32, proto string) []procNetEntry {
	path := fmt.Sprintf("/proc/%d/net/%s", pid, proto)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 {
		return nil
	}

	isV6 := proto == "tcp6" || proto == "udp6"
	out := make([]procNetEntry, 0, len(lines)-1)

	for _, line := range lines[1:] { // skip header
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		addr, port, ok := parseHexAddrPort(fields[1], isV6)
		if !ok {
			continue
		}
		ino, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}

		out = append(out, procNetEntry{
			addr:  addr,
			port:  port,
			state: fields[3],
			inode: ino,
		})
	}
	return out
}

// parseHexAddrPort parses entries like "0100007F:1F90" (IPv4) or
// "00000000000000000000000000000000:1F90" (IPv6). The address portion
// is in little-endian hex; the port is big-endian hex.
func parseHexAddrPort(s string, isV6 bool) (string, uint16, bool) {
	rawAddr, rawPort, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, false
	}

	port64, err := strconv.ParseUint(rawPort, 16, 16)
	if err != nil {
		return "", 0, false
	}

	var addr string
	if isV6 {
		if len(rawAddr) != 32 {
			return "", 0, false
		}
		addr = formatIPv6(rawAddr)
	} else {
		if len(rawAddr) != 8 {
			return "", 0, false
		}
		a, ok := formatIPv4(rawAddr)
		if !ok {
			return "", 0, false
		}
		addr = a
	}
	return addr, uint16(port64), true
}

// formatIPv4 decodes a little-endian hex IPv4 address like "0100007F"
// into "127.0.0.1".
func formatIPv4(hex string) (string, bool) {
	b := make([]byte, 4)
	for i := range 4 {
		v, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return "", false
		}
		b[3-i] = byte(v) // little-endian → reverse
	}
	return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3]), true
}

// formatIPv6 decodes a 32-char hex IPv6 address from /proc/net/tcp6.
// The kernel writes four 32-bit little-endian words, so each 8-char
// group must be byte-reversed before being laid out as standard IPv6.
func formatIPv6(hex string) string {
	bytes := make([]byte, 16)
	for word := range 4 {
		for i := range 4 {
			v, err := strconv.ParseUint(hex[word*8+i*2:word*8+i*2+2], 16, 8)
			if err != nil {
				return ""
			}
			bytes[word*4+(3-i)] = byte(v)
		}
	}

	parts := make([]string, 8)
	for i := range 8 {
		parts[i] = fmt.Sprintf("%x", uint16(bytes[i*2])<<8|uint16(bytes[i*2+1]))
	}
	return strings.Join(parts, ":")
}
