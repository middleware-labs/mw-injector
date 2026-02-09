package otelinject

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultNodeAgentBasePath = "/usr/lib/opentelemetry/nodejs"
)

// ValidateNodeAgent checks whether the OpenTelemetry Node.js
// auto-instrumentation agent is present and ready to be used.
func ValidateNodeAgent(basePath string) NodeAgentStatus {
	if basePath == "" {
		basePath = DefaultNodeAgentBasePath
	}

	status := NodeAgentStatus{Ready: true}

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
		status.Ready = false
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
		status.Ready = false
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
			status.Ready = false
		}
	}

	if len(status.MissingDeps) > 0 {
		status.Errors = append(status.Errors,
			fmt.Sprintf("missing required node_modules: %v", status.MissingDeps))
	}

	return status
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
