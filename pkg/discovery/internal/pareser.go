package internal

import (
	"regexp"
	"strings"
)

// ParseCommandLine parses a command line string into individual arguments
// This handles quoted arguments and escaped characters properly
func ParseCommandLine(cmdline string) []string {
	if cmdline == "" {
		return []string{}
	}

	var args []string
	var current strings.Builder
	var inQuotes bool
	var quoteChar rune

	// Handle null-separated arguments (common in /proc/*/cmdline)
	if strings.Contains(cmdline, "\x00") {
		parts := strings.Split(cmdline, "\x00")
		// Remove empty strings
		for _, part := range parts {
			if part != "" {
				args = append(args, part)
			}
		}
		return args
	}

	runes := []rune(cmdline)
	for i := 0; i < len(runes); i++ {
		char := runes[i]

		switch {
		case char == '\\' && i+1 < len(runes):
			// Handle escape sequences
			next := runes[i+1]
			current.WriteRune(next)
			i++ // Skip the next character

		case (char == '"' || char == '\'') && !inQuotes:
			// Start of quoted string
			inQuotes = true
			quoteChar = char

		case char == quoteChar && inQuotes:
			// End of quoted string
			inQuotes = false
			quoteChar = 0

		case char == ' ' && !inQuotes:
			// Argument separator
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}

		default:
			// Regular character
			current.WriteRune(char)
		}
	}

	// Add the last argument if any
	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}

// ExtractJarFiles finds all JAR files mentioned in command arguments
func ExtractJarFiles(args []string) []string {
	var jarFiles []string

	for _, arg := range args {
		if strings.HasSuffix(strings.ToLower(arg), ".jar") {
			jarFiles = append(jarFiles, arg)
		}
	}

	return jarFiles
}

// ExtractSystemProperties extracts all -D system properties from arguments
func ExtractSystemProperties(args []string) map[string]string {
	properties := make(map[string]string)

	for _, arg := range args {
		if strings.HasPrefix(arg, "-D") {
			prop := strings.TrimPrefix(arg, "-D")
			if idx := strings.Index(prop, "="); idx != -1 {
				key := prop[:idx]
				value := prop[idx+1:]
				properties[key] = value
			} else {
				// Property without value (like -Ddebug)
				properties[prop] = "true"
			}
		}
	}

	return properties
}

// ExtractJVMOptions extracts JVM-specific options from arguments
func ExtractJVMOptions(args []string) []string {
	var jvmOptions []string

	jvmPrefixes := []string{
		"-X",   // Extended options like -Xmx, -Xms
		"-XX:", // Advanced options like -XX:+UseG1GC
		"-D",   // System properties
		"-javaagent:",
		"-verbose:",
		"-ea", "-enableassertions",
		"-da", "-disableassertions",
		"-server", "-client",
	}

	for _, arg := range args {
		isJVMOption := false

		for _, prefix := range jvmPrefixes {
			if strings.HasPrefix(arg, prefix) {
				isJVMOption = true
				break
			}
		}

		if isJVMOption {
			jvmOptions = append(jvmOptions, arg)
		}
	}

	return jvmOptions
}

// FindMainClass attempts to identify the main class from command arguments
func FindMainClass(args []string) string {
	// Skip JVM options and find the main class
	for i, arg := range args {
		// Skip the java executable itself
		if i == 0 && strings.Contains(arg, "java") {
			continue
		}

		// Skip JVM options
		if strings.HasPrefix(arg, "-") {
			// Handle -jar specially
			if arg == "-jar" && i+1 < len(args) {
				// Next argument is the jar file, not main class
				return ""
			}
			continue
		}

		// Skip jar files
		if strings.HasSuffix(strings.ToLower(arg), ".jar") {
			continue
		}

		// This might be a main class if it contains dots
		if strings.Contains(arg, ".") && isValidClassName(arg) {
			return arg
		}
	}

	return ""
}

// isValidClassName checks if a string looks like a valid Java class name
func isValidClassName(name string) bool {
	// Basic validation for Java class names
	// Should contain only letters, numbers, dots, and underscores
	// Should not start with a number
	matched, _ := regexp.MatchString(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$`, name)
	return matched
}

// ExtractSpringBootInfo extracts Spring Boot specific information
func ExtractSpringBootInfo(args []string) map[string]string {
	info := make(map[string]string)

	springBootPrefixes := []string{
		"-Dspring.application.name=",
		"-Dspring.profiles.active=",
		"-Dserver.port=",
		"-Dspring.config.location=",
		"-Dlogging.level.=",
	}

	for _, arg := range args {
		for _, prefix := range springBootPrefixes {
			if strings.HasPrefix(arg, prefix) {
				key := strings.TrimSuffix(strings.TrimPrefix(prefix, "-D"), "=")
				value := strings.TrimPrefix(arg, prefix)
				info[key] = value
				break
			}
		}
	}

	return info
}

// NormalizeServiceName normalizes a service name according to common conventions
func NormalizeServiceName(name string) string {
	if name == "" {
		return ""
	}

	// Convert to lowercase
	name = strings.ToLower(name)

	// Replace common separators with hyphens
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, ".", "-")

	// Remove invalid characters (keep only alphanumeric and hyphens)
	reg := regexp.MustCompile(`[^a-z0-9\-]+`)
	name = reg.ReplaceAllString(name, "")

	// Remove leading/trailing hyphens
	name = strings.Trim(name, "-")

	// Collapse multiple consecutive hyphens
	reg = regexp.MustCompile(`-+`)
	name = reg.ReplaceAllString(name, "-")

	return name
}

// IsGenericName checks if a name is too generic to be useful as a service name
func IsGenericName(name string) bool {
	genericNames := []string{
		"java", "app", "application", "service", "server", "main",
		"demo", "test", "example", "sample", "hello", "world",
		"microservice", "ms", "api", "web", "webapp", "backend",
		"frontend", "client", "admin", "console", "dashboard",
	}

	nameLower := strings.ToLower(name)
	for _, generic := range genericNames {
		if nameLower == generic {
			return true
		}
	}

	return false
}
