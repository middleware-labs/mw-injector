// package cli

// import "fmt"

// // PrintUsage prints the main usage information
// func PrintUsage() {
// 	fmt.Println(`MW Injector Manager
// Usage:
//   mw-injector list                          List all Java processes (host)
//   mw-injector list-docker                   List all Java Docker containers
//   mw-injector list-all                      List both host processes and Docker containers
//   mw-injector auto-instrument               Auto-instrument all uninstrumented processes (host)
//   mw-injector instrument-docker             Auto-instrument all Java Docker containers
//   mw-injector instrument-container <name>   Instrument specific Docker container
//   mw-injector uninstrument                  Uninstrument all host processes
//   mw-injector uninstrument-docker           Uninstrument all Docker containers
//   mw-injector uninstrument-container <name> Uninstrument specific Docker container

// Examples:
//   # Host Java processes
//   sudo mw-injector list
//   sudo mw-injector auto-instrument

//   # Docker containers
//   sudo mw-injector list-docker
//   sudo mw-injector instrument-docker
//   sudo mw-injector instrument-container my-java-app
//   sudo mw-injector uninstrument-container my-java-app

//   # List everything
//   sudo mw-injector list-all`)
// }

// // GetCommandDescription returns a description for the given command
// func GetCommandDescription(command string) string {
// 	descriptions := map[string]string{
// 		"list":                   "List all Java processes running on the host",
// 		"list-docker":            "List all Java Docker containers",
// 		"list-all":               "List both host processes and Docker containers",
// 		"auto-instrument":        "Auto-instrument all uninstrumented Java processes on the host",
// 		"instrument-docker":      "Auto-instrument all Java Docker containers",
// 		"instrument-container":   "Instrument a specific Docker container",
// 		"uninstrument":           "Uninstrument all host Java processes",
// 		"uninstrument-docker":    "Uninstrument all Docker containers",
// 		"uninstrument-container": "Uninstrument a specific Docker container",
// 	}

// 	if desc, exists := descriptions[command]; exists {
// 		return desc
// 	}
// 	return "Unknown command"
// }

package cli

import "fmt"

// PrintUsage prints the main usage information
func PrintUsage() {
	fmt.Println(`MW Injector Manager
Usage:
  mw-injector list-all                      List all Java processes (Tomcat, Systemd, Docker)
  mw-injector list-systemd                  List systemd Java processes
  mw-injector list-docker                   List all Java Docker containers
  mw-injector auto-instrument               Auto-instrument all uninstrumented processes (host)
  mw-injector instrument-docker             Auto-instrument all Java Docker containers
  mw-injector instrument-container <name>   Instrument specific Docker container
  mw-injector uninstrument                  Uninstrument all host processes
  mw-injector uninstrument-docker           Uninstrument all Docker containers
  mw-injector uninstrument-container <name> Uninstrument specific Docker container

Examples:
  # List all processes (gorgeous segregated view)
  sudo mw-injector list-all

  # List specific types
  sudo mw-injector list-systemd
  sudo mw-injector list-docker

  # Instrument processes
  sudo mw-injector auto-instrument
  sudo mw-injector instrument-docker
  sudo mw-injector instrument-container my-java-app

  # Uninstrument
  sudo mw-injector uninstrument
  sudo mw-injector uninstrument-docker
  sudo mw-injector uninstrument-container my-java-app`)
}

// GetCommandDescription returns a description for the given command
func GetCommandDescription(command string) string {
	descriptions := map[string]string{
		"list-all":               "List all Java processes segregated by type (Tomcat, Systemd, Docker)",
		"list-systemd":           "List systemd Java processes on the host",
		"list-docker":            "List all Java Docker containers",
		"auto-instrument":        "Auto-instrument all uninstrumented Java processes on the host",
		"instrument-docker":      "Auto-instrument all Java Docker containers",
		"instrument-container":   "Instrument a specific Docker container",
		"uninstrument":           "Uninstrument all host Java processes",
		"uninstrument-docker":    "Uninstrument all Docker containers",
		"uninstrument-container": "Uninstrument a specific Docker container",
	}

	if desc, exists := descriptions[command]; exists {
		return desc
	}
	return "Unknown command"
}
