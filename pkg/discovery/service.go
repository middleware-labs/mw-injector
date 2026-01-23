package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func (d *discoverer) extractServiceName(javaProc *JavaProcess, cmdArgs []string) {
	// --- Level 1: Infrastructure (Container) ---
	if javaProc.IsInContainer() && javaProc.ContainerInfo.ContainerName != "" {
		javaProc.ServiceName = javaProc.ContainerInfo.ContainerName
		return
	}

	// --- Level 2: Explicit Environment (OTel Standards) ---
	// Check /proc/PID/environ for OTEL_SERVICE_NAME
	if envName := d.extractFromEnviron(javaProc.ProcessPID); envName != "" {
		javaProc.ServiceName = d.cleanServiceName(envName)
		return
	}

	// --- Level 3: Systemd Unit Name (High Confidence for Host) ---
	// NEW: If it's a systemd service, use the unit name!
	if unitName := d.extractSystemdUnitName(javaProc.ProcessPID); unitName != "" {
		javaProc.ServiceName = d.cleanServiceName(unitName)
		return
	}

	// --- Level 4: Java System Properties (-Dservice.name, etc.) ---
	if propName := d.extractFromSystemProperties(cmdArgs); propName != "" {
		javaProc.ServiceName = propName
		return
	}

	// --- Level 5: Application Identity (JAR/Main Class) ---
	if javaProc.JarFile != "" {
		name := d.extractFromJarName(javaProc.JarFile)
		if !d.isGenericJavaName(name) {
			javaProc.ServiceName = name
			return
		}
	}

	if javaProc.MainClass != "" {
		name := d.extractFromMainClass(javaProc.MainClass)
		if !d.isGenericJavaName(name) {
			javaProc.ServiceName = name
			return
		}
	}

	// --- Level 6: Directory structure (Last Resort) ---
	if javaProc.JarPath != "" {
		name := d.extractFromDirectory(javaProc.JarPath)
		if !d.isGenericJavaName(name) {
			javaProc.ServiceName = name
			return
		}
	}

	javaProc.ServiceName = "java-service"
}

func (d *discoverer) extractSystemdUnitName(pid int32) string {
	path := fmt.Sprintf("/proc/%d/cgroup", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		// Look for any line containing ".service"
		if strings.Contains(line, ".service") {
			// Split by '/' to get the last part: e.g., "book-service-java.service"
			parts := strings.Split(line, "/")
			unit := parts[len(parts)-1]

			// In some cgroup v2 formats, it might look like "book.service/some-sub-task"
			// So we take the first part of that specific segment
			if strings.Contains(unit, ".service") {
				unitParts := strings.Split(unit, ".service")
				// Return the name before ".service"
				return unitParts[0]
			}
		}
	}
	return ""
}

func (d *discoverer) isGenericJavaName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	generics := map[string]bool{
		"bin": true, "lib": true, "src": true, "target": true,
		"java": true, "jre": true, "jdk": true, "app": true,
		"main": true, "server": true, "tomcat": true,
	}
	return generics[name] || name == "." || name == ""
}

// extractFromSystemProperties looks for service name in JVM system properties
func (d *discoverer) extractFromSystemProperties(cmdArgs []string) string {
	// Common system properties that contain service names
	serviceProperties := []string{
		"-Dotel.service.name=",
		"-Dservice.name=",
		"-Dspring.application.name=",
		"-Dapplication.name=",
		"-Dmw.service.name=",
		"-DOTEL_SERVICE_NAME=",
		"-DSERVICE_NAME=",
	}

	for _, arg := range cmdArgs {
		for _, prop := range serviceProperties {
			if strings.HasPrefix(arg, prop) {
				serviceName := strings.TrimPrefix(arg, prop)
				serviceName = strings.Trim(serviceName, `"'`)
				if serviceName != "" {
					return d.cleanServiceName(serviceName)
				}
			}
		}
	}

	return ""
}

// extractFromJarName extracts service name from JAR file name
func (d *discoverer) extractFromJarName(jarFile string) string {
	if jarFile == "" {
		return ""
	}

	// Remove path and extension
	baseName := filepath.Base(jarFile)
	nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))

	// Common patterns to clean up
	patterns := []struct {
		regex       *regexp.Regexp
		replacement string
	}{
		// Remove version numbers: app-1.2.3 -> app
		{regexp.MustCompile(`-\d+\.\d+\.\d+.*$`), ""},
		{regexp.MustCompile(`-\d+\.\d+.*$`), ""},
		{regexp.MustCompile(`_\d+\.\d+\.\d+.*$`), ""},
		{regexp.MustCompile(`_\d+\.\d+.*$`), ""},

		// Remove SNAPSHOT: app-SNAPSHOT -> app
		{regexp.MustCompile(`-SNAPSHOT$`), ""},
		{regexp.MustCompile(`_SNAPSHOT$`), ""},

		// Remove BUILD numbers: app-BUILD-123 -> app
		{regexp.MustCompile(`-BUILD-\d+$`), ""},
		{regexp.MustCompile(`_BUILD_\d+$`), ""},

		// Remove common suffixes
		{regexp.MustCompile(`-service$`), ""},
		{regexp.MustCompile(`-app$`), ""},
		{regexp.MustCompile(`-application$`), ""},
		{regexp.MustCompile(`-microservice$`), ""},
		{regexp.MustCompile(`-ms$`), ""},
	}

	serviceName := nameWithoutExt
	for _, pattern := range patterns {
		serviceName = pattern.regex.ReplaceAllString(serviceName, pattern.replacement)
	}

	return d.cleanServiceName(serviceName)
}

// extractFromDirectory extracts service name from directory structure
func (d *discoverer) extractFromDirectory(jarPath string) string {
	if jarPath == "" {
		return ""
	}

	dir := filepath.Dir(jarPath)
	pathParts := strings.Split(dir, "/")

	// Look for meaningful directory names
	meaningfulDirs := []string{}
	for _, part := range pathParts {
		part = strings.TrimSpace(part)
		if part != "" && !d.isGenericDir(part) {
			meaningfulDirs = append(meaningfulDirs, part)
		}
	}

	// Use the last meaningful directory
	if len(meaningfulDirs) > 0 {
		serviceName := meaningfulDirs[len(meaningfulDirs)-1]
		return d.cleanServiceName(serviceName)
	}

	return ""
}

// extractFromMainClass extracts service name from main class name
func (d *discoverer) extractFromMainClass(mainClass string) string {
	if mainClass == "" {
		return ""
	}

	// Split by package separators
	parts := strings.Split(mainClass, ".")
	if len(parts) == 0 {
		return ""
	}

	// Use the last part (class name)
	className := parts[len(parts)-1]

	// Remove common suffixes
	patterns := []struct {
		regex       *regexp.Regexp
		replacement string
	}{
		{regexp.MustCompile(`Application$`), ""},
		{regexp.MustCompile(`App$`), ""},
		{regexp.MustCompile(`Service$`), ""},
		{regexp.MustCompile(`Server$`), ""},
		{regexp.MustCompile(`Main$`), ""},
		{regexp.MustCompile(`Launcher$`), ""},
		{regexp.MustCompile(`Bootstrap$`), ""},
	}

	serviceName := className
	for _, pattern := range patterns {
		serviceName = pattern.regex.ReplaceAllString(serviceName, pattern.replacement)
	}

	// Convert CamelCase to kebab-case
	serviceName = d.camelToKebab(serviceName)

	return d.cleanServiceName(serviceName)
}

// extractFromProcessName extracts service name from process executable name
func (d *discoverer) extractFromProcessName(execName string) string {
	if execName == "" || execName == "java" {
		return ""
	}

	return d.cleanServiceName(execName)
}

// cleanServiceName applies final cleaning to service name
func (d *discoverer) cleanServiceName(name string) string {
	if name == "" {
		return ""
	}

	// Convert to lowercase
	name = strings.ToLower(name)

	// Replace underscores with hyphens
	name = strings.ReplaceAll(name, "_", "-")

	// Remove invalid characters (keep only alphanumeric and hyphens)
	reg := regexp.MustCompile(`[^a-z0-9\-]+`)
	name = reg.ReplaceAllString(name, "")

	// Remove leading/trailing hyphens
	name = strings.Trim(name, "-")

	// Collapse multiple consecutive hyphens
	reg = regexp.MustCompile(`-+`)
	name = reg.ReplaceAllString(name, "-")

	// Final validation - must not be empty and not be generic
	if name == "" || d.isGenericServiceName(name) {
		return ""
	}

	return name
}

// isGenericDir checks if a directory name is too generic to be useful
func (d *discoverer) isGenericDir(dir string) bool {
	genericDirs := []string{
		"", ".", "..", "/", "home", "opt", "usr", "var", "tmp", "app", "apps",
		"bin", "lib", "lib64", "java", "jvm", "target", "build", "classes",
		"WEB-INF", "META-INF", "src", "main", "resources", "static", "public",
	}

	dirLower := strings.ToLower(dir)
	for _, generic := range genericDirs {
		if dirLower == generic {
			return true
		}
	}

	return false
}

// isGenericServiceName checks if a service name is too generic
func (d *discoverer) isGenericServiceName(name string) bool {
	genericNames := []string{
		"java", "app", "application", "service", "server", "main",
		"demo", "test", "example", "sample", "hello", "world",
	}

	nameLower := strings.ToLower(name)
	for _, generic := range genericNames {
		if nameLower == generic {
			return true
		}
	}

	return false
}

// camelToKebab converts CamelCase to kebab-case
func (d *discoverer) camelToKebab(s string) string {
	// Insert hyphens before uppercase letters (except the first character)
	reg := regexp.MustCompile(`([a-z])([A-Z])`)
	s = reg.ReplaceAllString(s, "${1}-${2}")

	return strings.ToLower(s)
}

// extractJavaInfo extracts Java-specific information from command arguments
func (d *discoverer) extractJavaInfo(javaProc *JavaProcess, cmdArgs []string) {
	var jvmOptions []string
	var jarFile string
	var jarPath string
	var mainClass string

	for i, arg := range cmdArgs {
		// JVM options (start with -)
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "-jar") {
			// Skip -jar flag itself
			if arg != "-jar" {
				jvmOptions = append(jvmOptions, arg)
			}
		}

		// JAR file detection
		if arg == "-jar" && i+1 < len(cmdArgs) {
			jarPath = cmdArgs[i+1]
			jarFile = filepath.Base(jarPath)
		}

		// Main class detection (not starting with - and not a .jar file)
		if !strings.HasPrefix(arg, "-") && !strings.HasSuffix(arg, ".jar") &&
			!strings.Contains(arg, "/") && strings.Contains(arg, ".") {
			// This looks like a main class (contains dots but no slashes)
			mainClass = arg
		}
	}

	javaProc.JVMOptions = jvmOptions
	javaProc.JarFile = jarFile
	javaProc.JarPath = jarPath
	javaProc.MainClass = mainClass
}
