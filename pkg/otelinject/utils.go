package otelinject

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/k0kubun/pp"
)

const (
	DefaultNodeAgentBasePath   = "/usr/lib/opentelemetry/nodejs"
	DefaultLibOtelInjectorPath = "/usr/lib/opentelemetry/libotelinject.so"
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
		pp.Println("register.js entry point found: %v\n", registerJS)
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
		pp.Printf("could read package.json: %v\n", ver)
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
	pp.Println(status)
	if len(status.Errors) > 0 {
		status.Ready = false
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

func ldPreloadSharedObjectPresent() error {
	if _, err := os.Stat(DefaultLibOtelInjectorPath); err != nil {
		return fmt.Errorf("libotelinject.so not found at %s: %w", DefaultLibOtelInjectorPath, err)
	}
	return nil
}
