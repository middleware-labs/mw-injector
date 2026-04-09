//go:build !cgo

package discovery

// clockTickHz is the system's clock tick frequency (USER_HZ), used to convert
// jiffies from /proc/<pid>/stat to milliseconds.
//
// When cgo is disabled we cannot call sysconf(_SC_CLK_TCK), so we fall back to
// 100 Hz — the stable Linux USER_HZ ABI value used on all standard kernels.
var clockTickHz int64 = 100
