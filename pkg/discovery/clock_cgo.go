//go:build cgo

package discovery

/*
#include <unistd.h>
*/
import "C"

// clockTickHz is the system's clock tick frequency (USER_HZ), used to convert
// jiffies from /proc/<pid>/stat to milliseconds. Queried once at init time
// via sysconf(_SC_CLK_TCK). Falls back to 100 Hz if the syscall fails.
//
// On Linux, USER_HZ is virtually always 100 (it's a stable kernel ABI), but
// querying at runtime avoids any platform assumptions.
var clockTickHz int64

func init() {
	hz := int64(C.sysconf(C._SC_CLK_TCK))
	if hz <= 0 {
		hz = 100
	}
	clockTickHz = hz
}
