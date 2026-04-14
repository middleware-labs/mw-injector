// service.go contains systemd-related helpers for the discovery package:
// ignored unit lists, cgroup parsing for unit name extraction, and systemd
// status checking.
package discovery

// ignoredSystemdUnits is the set of systemd unit names whose processes
// should be excluded from discovery entirely (desktop/GUI services, etc.).
var ignoredSystemdUnits = map[string]bool{
	"plasma-plasmashell": true,
	"gnome-shell":        true,
	"gnome-terminal":     true,
	"xfce4-session":      true,
	"dbus":               true,
	"systemd":            true,
	"init":               true,
}
