package otelinject

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	DefaultNodeAgentBasePath   = "/usr/lib/opentelemetry/nodejs"
	DefaultLibOtelInjectorPath = "/usr/lib/opentelemetry/libotelinject.so"
	DefaultJavaAgentBasePath   = "/usr/lib/opentelemetry/jvm"
	DefaultPythonAgentBasePath = "/opt/otel-python-agent"
)

// ValidateNodeAgent checks whether the OpenTelemetry Node.js
// auto-instrumentation agent is present and ready to be used.
func ValidateNodeAgent(basePath string) NodeAgentStatus {

	if basePath == "" {
		basePath = DefaultNodeAgentBasePath
	}

	status := NodeAgentStatus{Ready: true}

	if err := ldPreloadSharedObjectPresent(); err != nil {
		status.Errors = append(status.Errors, err.Error())
	} else {
		status.InjectorSharedObjectFound = true
	}
	// 1. The critical entry point that the injector loads via NODE_OPTIONS -r flag.
	//    Without this file, instrumentation does nothing.
	registerJS := filepath.Join(basePath,
		"node_modules",
		"@opentelemetry",
		"auto-instrumentations-node",
		"build", "src", "register.js",
	)
	if _, err := os.Stat(registerJS); err != nil {
		status.RegisterJSFound = false
		status.Errors = append(status.Errors,
			fmt.Sprintf("register.js entry point not found: %s", registerJS))
	} else {
		status.RegisterJSFound = true
	}

	// 2. Read the package version from auto-instrumentations-node/package.json
	pkgJSONPath := filepath.Join(basePath,
		"node_modules",
		"@opentelemetry",
		"auto-instrumentations-node",
		"package.json",
	)
	if ver, err := readPackageVersion(pkgJSONPath); err != nil {
		status.Errors = append(status.Errors,
			fmt.Sprintf("could not read package.json: %v", err))
	} else {
		status.PackageVersion = ver
	}

	// 3. Required dependencies that must be present for instrumentation to work:
	//    - require-in-the-middle: hooks into CommonJS require() calls
	//    - import-in-the-middle:  hooks into ESM import statements
	//    - @opentelemetry/sdk-node: the core SDK that wires everything together
	//    - @opentelemetry/api: the OTel API surface
	requiredDeps := []string{
		"require-in-the-middle",
		"import-in-the-middle",
		filepath.Join("@opentelemetry", "sdk-node"),
		filepath.Join("@opentelemetry", "api"),
	}

	nodeModules := filepath.Join(basePath, "node_modules")
	for _, dep := range requiredDeps {
		depDir := filepath.Join(nodeModules, dep)
		if _, err := os.Stat(depDir); err != nil {
			status.MissingDeps = append(status.MissingDeps, dep)
		}
	}

	if len(status.MissingDeps) > 0 {
		status.Errors = append(status.Errors,
			fmt.Sprintf("missing required node_modules: %v", status.MissingDeps))
	}
	if len(status.Errors) > 0 {
		status.Ready = false
	}
	return status
}

func ValidateJavaAgent(basePath string) JavaAgentStatus {
	if basePath == "" {
		basePath = DefaultJavaAgentBasePath
	}

	status := JavaAgentStatus{
		Ready: true,
	}

	if err := ldPreloadSharedObjectPresent(); err != nil {
		status.Errors = append(status.Errors, err.Error())
	} else {
		status.InjectorSharedObjectFound = true
	}

	javaAgentJar := filepath.Join(basePath, "javaagent.jar")
	if _, err := os.Stat(javaAgentJar); err != nil {
		status.Ready = false
		status.Errors = append(status.Errors, fmt.Sprintf("javaagent.jar not found at %s: %v", javaAgentJar, err))
	} else {
		status.JavaAgentJarFound = true
	}

	if len(status.Errors) > 0 {
		status.Ready = false
	}

	return status
}

// ValidatePythonAgent checks if the Python agent is correctly set up for the CURRENT system.
func ValidatePythonAgent(basePath string) PythonAgentStatus {
	if basePath == "" {
		basePath = DefaultPythonAgentBasePath
	}

	status := PythonAgentStatus{Ready: true}

	// 1. Check for the Injector
	if err := ldPreloadSharedObjectPresent(); err != nil {
		status.Errors = append(status.Errors, err.Error())
	} else {
		status.InjectorSharedObjectFound = true
	}

	// 2. Detect System Libc Flavor (glibc or musl)
	flavor, err := getLibcFlavor()
	if err != nil {
		status.LibcFlavor = "unknown"
		status.Errors = append(status.Errors, fmt.Sprintf("Could not detect libc flavor: %v", err))
		status.Ready = false
		return status
	}
	status.LibcFlavor = flavor

	// 3. Check for the specific flavor directory
	// The injector expects /basePath/glibc or /basePath/musl
	flavorPath := filepath.Join(basePath, flavor)
	if info, err := os.Stat(flavorPath); err != nil || !info.IsDir() {
		status.TargetDirFound = false
		status.Errors = append(status.Errors, fmt.Sprintf("Agent directory for %s not found at: %s", flavor, flavorPath))
		status.Ready = false
		return status
	}
	status.TargetDirFound = true

	// 4. CRITICAL: Check for 'sitecustomize.py' (The Bootstrapper)
	// Without this, OTel will be present but dormant.
	bootstrapper := filepath.Join(flavorPath, "sitecustomize.py")
	if _, err := os.Stat(bootstrapper); err != nil {
		status.SiteCustomizeFound = false
		status.Errors = append(status.Errors, fmt.Sprintf("Missing 'sitecustomize.py' in %s. Python will not auto-start OTel without this.", flavorPath))
	} else {
		status.SiteCustomizeFound = true
	}

	// 5. Check for OTel libraries (The "Bullets")
	// We check for the 'opentelemetry' folder which pip installs.
	otelLibPath := filepath.Join(flavorPath, "opentelemetry")
	if _, err := os.Stat(otelLibPath); err != nil {
		status.Errors = append(status.Errors, fmt.Sprintf("OpenTelemetry libraries not found in %s. Did you run pip install --target?", flavorPath))
	}

	if len(status.Errors) > 0 {
		status.Ready = false
	}
	return status
}

// getLibcFlavor attempts to detect if the system is using glibc or musl
func getLibcFlavor() (string, error) {
	// Method 1: Check for ldd (Standard for glibc)
	// 'ldd --version' usually prints "ldd (GNU libc) ..." or "musl libc ..."
	cmd := exec.Command("ldd", "--version")
	out, err := cmd.Output()
	if err == nil {
		output := strings.ToLower(string(out))
		if strings.Contains(output, "gnu") || strings.Contains(output, "glibc") {
			return "glibc", nil
		}
		if strings.Contains(output, "musl") {
			return "musl", nil
		}
	}

	// Method 2: Check for existence of musl loader
	// Alpine and other musl distros often have this file
	// We can try to glob for it since the version number might change
	files, _ := filepath.Glob("/lib/ld-musl-*.so.1")
	if len(files) > 0 {
		return "musl", nil
	}

	// Method 3: Default assumption for most Linux server distros
	// If we are on Linux and it's not obviously musl, it's highly likely glibc.
	// But let's return error to be safe if strictly unsure.
	return "glibc", nil // Fallback to glibc as it's the 99% case for non-Alpine
}

// readPackageVersion reads the "version" field from a package.json file.
func readPackageVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", fmt.Errorf("invalid package.json: %w", err)
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("version field is empty in %s", path)
	}

	return pkg.Version, nil
}

func ldPreloadSharedObjectPresent() error {
	if _, err := os.Stat(DefaultLibOtelInjectorPath); err != nil {
		return fmt.Errorf("libotelinject.so not found at %s: %w", DefaultLibOtelInjectorPath, err)
	}
	return nil
}

func checkSystemdStatus(pid int32) (bool, string) {
	path := fmt.Sprintf("/proc/%d/cgroup", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		// Only look at the main systemd hierarchy or unified hierarchy (0::)
		if !strings.Contains(line, ":name=systemd:") && !strings.HasPrefix(line, "0::") {
			continue
		}

		// Extract the path part (everything after the second colon)
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		cgroupPath := parts[2]

		// Split path into segments: /, user.slice, user@1000.service, app.slice, my-app.service
		segments := strings.Split(cgroupPath, "/")

		// REVERSE SEARCH: Find the *last* segment ending in .service
		for i := len(segments) - 1; i >= 0; i-- {
			segment := segments[i]
			if strings.HasSuffix(segment, ".service") {
				// FILTER: Ignore the generic user session service
				if strings.HasPrefix(segment, "user@") {
					continue
				}

				// Found a real service!
				unitName := strings.TrimSuffix(segment, ".service")
				return true, unitName
			}
		}
	}
	return false, ""
}
