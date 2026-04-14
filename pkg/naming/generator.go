// Package naming provides service name generation with sanitization rules.
// It derives a human-readable service name from a discovered process using
// a multi-level heuristic (container name, env vars, systemd unit, JAR
// filename, directory structure).
package naming

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// GenerateServiceName generates a service name for a Java process.
func GenerateServiceName(proc *discovery.Process) string {
	if proc.DetailBool(discovery.DetailIsTomcat) {
		return GenerateForTomcat(proc)
	}

	return GenerateForStandard(proc)
}

// GenerateForTomcat generates service names for Tomcat processes.
func GenerateForTomcat(proc *discovery.Process) string {
	tomcatInfo := proc.ExtractTomcatInfo()

	// Get instance name from CATALINA_BASE
	instanceName := filepath.Base(filepath.Dir(tomcatInfo.CatalinaBase))

	// Fallback: try CATALINA_BASE itself
	if instanceName == "." || instanceName == "/" || instanceName == "opt" {
		instanceName = filepath.Base(tomcatInfo.CatalinaBase)
	}

	instanceName = CleanTomcatInstance(instanceName)

	if instanceName == "" || instanceName == "tomcat" {
		instanceName = "default"
	}

	return fmt.Sprintf("tomcat-%s", instanceName)
}

// GenerateForStandard generates service names for standard Java processes.
func GenerateForStandard(proc *discovery.Process) string {
	jarFile := proc.DetailString(discovery.DetailJarFile)
	if jarFile != "" {
		cleaned := CleanJarName(jarFile)
		if cleaned != "" {
			return cleaned
		}
	}

	if proc.ServiceName != "" && proc.ServiceName != "java-service" {
		cleaned := CleanServiceName(proc.ServiceName)
		if cleaned != "" {
			return cleaned
		}
	}

	return fmt.Sprintf("java-app-%d", proc.PID)
}

// GenerateWithOptions generates a service name with custom options.
func GenerateWithOptions(proc *discovery.Process, opts ServiceNameOptions) string {
	// If a preferred name is provided, try to use it
	if opts.PreferredName != "" {
		cleaned := CleanServiceName(opts.PreferredName)
		if cleaned != "" && (opts.AllowGeneric || !IsGenericName(cleaned)) {
			return cleaned
		}
	}

	// Fall back to standard generation
	switch opts.ServiceType {
	case ServiceTypeTomcat:
		return GenerateForTomcat(proc)
	case ServiceTypeStandard:
		return GenerateForStandard(proc)
	default:
		return GenerateServiceName(proc)
	}
}

// ValidateServiceName checks if a service name meets naming requirements
func ValidateServiceName(name string) error {
	if name == "" {
		return fmt.Errorf("service name cannot be empty")
	}

	// Check for invalid characters
	if strings.ContainsAny(name, " _./\\:*?\"<>|") {
		return fmt.Errorf("service name contains invalid characters")
	}

	// Check if it's too generic
	if IsGenericName(name) {
		return fmt.Errorf("service name '%s' is too generic", name)
	}

	// Check length constraints
	if len(name) > 200 {
		return fmt.Errorf("service name is too long (max 200 characters)")
	}

	if len(name) < 1 {
		return fmt.Errorf("service name is too short")
	}

	return nil
}

// SuggestAlternativeName suggests an alternative name if the current one is invalid.
func SuggestAlternativeName(proc *discovery.Process, invalidName string) string {
	alternatives := []string{}

	jarFile := proc.DetailString(discovery.DetailJarFile)
	if jarFile != "" {
		if alt := CleanJarName(jarFile); alt != "" && alt != invalidName {
			alternatives = append(alternatives, alt)
		}
	}

	if proc.ServiceName != "" && proc.ServiceName != "java-service" {
		if alt := CleanServiceName(proc.ServiceName); alt != "" && alt != invalidName {
			alternatives = append(alternatives, alt)
		}
	}

	for _, alt := range alternatives {
		if ValidateServiceName(alt) == nil {
			return alt
		}
	}

	return fmt.Sprintf("java-service-%d", proc.PID)
}
