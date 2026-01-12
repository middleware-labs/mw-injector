package discovery

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type DiscoveryCandidate struct {
	Process       *process.Process
	Exe           string
	Cmdline       string
	Args          []string
	PPid          int32
	Owner         string
	IsJavaProcess bool
	CreateTime    int64
	Status        []string
}

// discoverer implements the Discoverer interface
type discoverer struct {
	ctx               context.Context
	opts              DiscoveryOptions
	containerDetector *ContainerDetector
	userCache         sync.Map
}

// DiscoverJavaProcesses finds all Java processes with default options
func (d *discoverer) DiscoverJavaProcesses(ctx context.Context) ([]JavaProcess, error) {
	return d.DiscoverWithOptions(ctx, d.opts)
}

// DiscoverWithOptions finds Java processes with custom configuration
func (d *discoverer) DiscoverWithOptions(ctx context.Context, opts DiscoveryOptions) ([]JavaProcess, error) {
	// Create context with timeout
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Get all processes
	allProcesses, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("failed to get process list: %w", err)
	}

	// Filter for Java processes first to reduce workload
	// javaDiscoverCandidates := d.filterJavaProcesses(allProcesses)

	// Process concurrently with worker pool
	return d.processWithWorkerPool(ctx, allProcesses, opts)
}

func (d *discoverer) DiscoverNodeWithOptions(
	ctx context.Context, opts DiscoveryOptions,
) ([]NodeProcess, error) {
	var cancel context.CancelFunc

	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	allProcesses, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("failed to get process list: %w", err)
	}

	nodeProcesses := d.filterNodeProcesses(allProcesses)
	return d.processNodeWithWorkerPool(ctx, nodeProcesses, opts)
}

func (d *discoverer) DiscoverPythonWithOptions(ctx context.Context, opts DiscoveryOptions) ([]PythonProcess, error) {
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	allProcesses, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("failed to get process list: %w", err)
	}

	// Filter for Python processes first
	var pythonCandidates []*process.Process
	for _, proc := range allProcesses {
		if d.isPythonProcess(proc) {
			pythonCandidates = append(pythonCandidates, proc)
		}
	}

	return d.processPythonWithWorkerPool(ctx, pythonCandidates, opts)
}

type pythonProcessResult struct {
	process *PythonProcess
	err     error
}

func (d *discoverer) processPythonWithWorkerPool(ctx context.Context, processes []*process.Process, opts DiscoveryOptions) ([]PythonProcess, error) {
	if len(processes) == 0 {
		return []PythonProcess{}, nil
	}

	jobs := make(chan *process.Process, len(processes))
	results := make(chan pythonProcessResult, len(processes))

	numWorkers := opts.MaxConcurrency
	if numWorkers <= 0 {
		numWorkers = 10
	}
	if numWorkers > len(processes) {
		numWorkers = len(processes)
	}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go d.pythonWorker(ctx, jobs, results, opts, &wg)
	}

	go func() {
		defer close(jobs)
		for _, proc := range processes {
			select {
			case jobs <- proc:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var pyProcesses []PythonProcess
	for result := range results {
		if result.err != nil {
			continue
		}
		if result.process != nil {
			if d.passesPythonFilter(*result.process, opts.Filter) {
				pyProcesses = append(pyProcesses, *result.process)
			}
		}
	}

	return pyProcesses, nil
}

func (d *discoverer) pythonWorker(ctx context.Context, jobs <-chan *process.Process, results chan<- pythonProcessResult, opts DiscoveryOptions, wg *sync.WaitGroup) {
	defer wg.Done()
	for proc := range jobs {
		select {
		case <-ctx.Done():
			results <- pythonProcessResult{nil, ctx.Err()}
			return
		default:
		}
		pyProc, err := d.processOnePython(ctx, proc, opts)
		results <- pythonProcessResult{pyProc, err}
	}
}

func (d *discoverer) processOnePython(ctx context.Context, proc *process.Process, opts DiscoveryOptions) (*PythonProcess, error) {
	pid := proc.Pid
	cmdline, _ := proc.Cmdline()
	exe, _ := proc.Exe()
	parentPID, _ := proc.Ppid()
	owner, _ := d.getProcessOwner(proc)
	createTime, _ := proc.CreateTime()
	status, _ := proc.Status()
	cmdArgs := d.parseCommandLine(cmdline)

	pyProc := &PythonProcess{
		ProcessPID:                pid,
		ProcessParentPID:          parentPID,
		ProcessExecutablePath:     exe,
		ProcessExecutableName:     filepath.Base(exe),
		ProcessCommandLine:        cmdline,
		ProcessCommandArgs:        cmdArgs,
		ProcessOwner:              owner,
		ProcessCreateTime:         time.Unix(createTime/1000, 0),
		Status:                    strings.Join(status, ","),
		ProcessRuntimeName:        "python",
		ProcessRuntimeDescription: "Python Interpreter",
	}

	// Container Detection
	if opts.IncludeContainerInfo {
		containerInfo, err := d.containerDetector.IsProcessInContainer(pyProc.ProcessPID)
		if err == nil && containerInfo.IsContainer {
			pyProc.ContainerInfo = containerInfo

			// CRITICAL: Fetch the name if it's missing
			if containerInfo.ContainerName == "" && containerInfo.ContainerID != "" {
				name := d.containerDetector.GetContainerNameByID(containerInfo.ContainerID, containerInfo.Runtime)
				if name != "" {
					pyProc.ContainerInfo.ContainerName = strings.TrimPrefix(name, "/")
				}
			}
		}
	}

	isSubProcess := strings.Contains(cmdline, "multiprocessing.spawn") ||
		strings.Contains(cmdline, "resource_tracker")
	if isSubProcess {
		// Option A: Return nil to skip reporting these entirely
		return nil, nil
	}
	// 1. Identify if this is a "Helper" or "Worker" process
	isWorker := strings.Contains(pyProc.ProcessCommandLine, "multiprocessing.spawn")
	isTracker := strings.Contains(pyProc.ProcessCommandLine, "multiprocessing.resource_tracker")

	// 2. Extract service name with Parent awareness
	d.extractPythonServiceName(pyProc, cmdArgs)

	// 3. SERVICE NAME LINKING LOGIC
	if isTracker {
		// These are purely internal to Python; often best to skip or name generically
		pyProc.ServiceName = "python-internal-tracker"
	} else if isWorker {
		// Try to find the parent's service name
		parentProc, err := process.NewProcess(pyProc.ProcessParentPID)
		if err == nil {
			parentCmd, _ := parentProc.Cmdline()
			parentArgs := d.parseCommandLine(parentCmd)

			// Temporary PythonProcess to run discovery on parent
			parentDummy := &PythonProcess{ProcessCommandLine: parentCmd}
			d.extractPythonServiceName(parentDummy, parentArgs)

			if parentDummy.ServiceName != "python-service" && parentDummy.ServiceName != "" {
				// Link child to parent name
				pyProc.ServiceName = parentDummy.ServiceName
			}
		}
	}

	// 1. Extract Entry Point & VirtualEnv
	d.extractPythonInfo(pyProc, cmdArgs)

	// 2. Extract Service Name (Logic similar to Node)
	d.extractPythonServiceName(pyProc, cmdArgs)

	// 3. Detect Framework/Manager (Gunicorn, Celery, etc.)
	d.detectPythonProcessManager(pyProc, cmdArgs)

	// 4. Detect Instrumentation
	d.detectPythonInstrumentation(pyProc, cmdArgs)

	return pyProc, nil
}

func (d *discoverer) extractPythonInfo(pyProc *PythonProcess, cmdArgs []string) {
	// Detect Virtual Environment from Executable Path
	if strings.Contains(pyProc.ProcessExecutablePath, "/bin/python") {
		pyProc.VirtualEnvPath = filepath.Dir(filepath.Dir(pyProc.ProcessExecutablePath))
	}

	for i, arg := range cmdArgs {
		if i == 0 || strings.HasPrefix(arg, "-") {
			continue
		}

		// Strategy: Find the first .py file or a module name after -m
		if strings.HasSuffix(arg, ".py") {
			pyProc.EntryPoint = arg
			if abs, err := filepath.Abs(arg); err == nil {
				pyProc.WorkingDirectory = filepath.Dir(abs)
			}
			break
		}

		// If using python -m <module>
		if i > 0 && cmdArgs[i-1] == "-m" {
			pyProc.ModulePath = arg
			break
		}
	}
}

func (d *discoverer) extractPythonServiceName(pyProc *PythonProcess, cmdArgs []string) {
	if pyProc.ContainerInfo != nil && pyProc.ContainerInfo.IsContainer {
		if pyProc.ContainerInfo.ContainerName != "" {
			pyProc.ServiceName = pyProc.ContainerInfo.ContainerName
			return
		}
	}
	// Strategy 1: Uvicorn/Gunicorn Module Pattern (e.g., "main:app")
	for _, arg := range cmdArgs {
		if strings.Contains(arg, ":") {
			// Check if previous arg was uvicorn/gunicorn or if the current arg is the 2nd/3rd
			parts := strings.Split(arg, ":")
			if len(parts) > 0 {
				potentialName := parts[0]
				// Avoid paths, just get the module name
				if strings.Contains(potentialName, "/") {
					potentialName = filepath.Base(potentialName)
				}

				if !d.isGenericPythonName(potentialName) {
					pyProc.ServiceName = d.cleanServiceName(potentialName)
					return
				}
			}
		}
	}

	// Strategy 2: Project Directory Detection (Highly Accurate for your case)
	// Looks for: /home/naman47/mw/apm/python/fastapi-bookstore-api/.env/bin/python3
	for _, arg := range cmdArgs {
		if strings.Contains(arg, "/.env/bin/") || strings.Contains(arg, "/venv/bin/") {
			// Split at the virtualenv marker
			pathParts := strings.Split(arg, "/.env/bin/")
			if len(pathParts) == 1 {
				pathParts = strings.Split(arg, "/venv/bin/")
			}

			// The directory immediately before .env is usually the project name
			if len(pathParts) > 0 {
				projectDir := filepath.Base(pathParts[0])
				if !d.isGenericPythonName(projectDir) {
					pyProc.ServiceName = projectDir
					return
				}
			}
		}
	}

	// Strategy 3: Uvicorn/Gunicorn Module name
	// Looks for "main:app" and returns "main"
	for _, arg := range cmdArgs {
		if strings.Contains(arg, ":") && !strings.Contains(arg, "/") {
			modulePart := strings.Split(arg, ":")[0]
			if !d.isGenericPythonName(modulePart) {
				pyProc.ServiceName = modulePart
				return
			}
		}
	}

	pyProc.ServiceName = "python-service"

	pyProc.ServiceName = "python-service"
}

func (d *discoverer) isGenericPythonName(name string) bool {
	generics := []string{
		"main", "app", "server", "index", "python", "python3",
		"uvicorn", "gunicorn", "multiprocessing", "resource_tracker",
	}
	nameLower := strings.ToLower(name)
	for _, g := range generics {
		if nameLower == g || strings.HasPrefix(nameLower, "python3") {
			return true
		}
	}
	return false
}

func (d *discoverer) detectPythonInstrumentation(pyProc *PythonProcess, cmdArgs []string) {
	cmdline := strings.Join(cmdArgs, " ")

	// Check for wrapper execution
	if strings.Contains(cmdline, "opentelemetry-instrument") {
		pyProc.HasPythonAgent = true
	}

	// Check environment for auto-instrumentation
	environPath := fmt.Sprintf("/proc/%d/environ", pyProc.ProcessPID)
	if data, err := os.ReadFile(environPath); err == nil {
		if strings.Contains(string(data), "PYTHONPATH") && strings.Contains(string(data), "mw_bootstrap") {
			pyProc.HasPythonAgent = true
			pyProc.IsMiddlewareAgent = true
		}
	}
}

func (d *discoverer) passesPythonFilter(proc PythonProcess, filter ProcessFilter) bool {
	if filter.CurrentUserOnly {
		return proc.ProcessOwner == os.Getenv("USER")
	}
	return true
}

// RefreshProcess updates information for a specific process
func (d *discoverer) RefreshProcess(ctx context.Context, pid int32) (*JavaProcess, error) {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("process %d not found: %w", pid, err)
	}

	discoveryCandidate := d.getJavaDiscoveryCandidateForProcesss(proc)
	// Check if it's a Java process
	if !discoveryCandidate.IsJavaProcess {
		return nil, fmt.Errorf("process %d is not a Java process", pid)
	}

	javaProc, err := d.processOne(ctx, &discoveryCandidate, d.opts)
	if err != nil {
		return nil, fmt.Errorf("failed to process PID %d: %w", pid, err)
	}

	return javaProc, nil
}

// Close cleans up any resources used by the discoverer
func (d *discoverer) Close() error {
	// No cleanup needed for current implementation
	return nil
}

// filterJavaProcesses quickly filters processes to find Java processes
func (d *discoverer) filterJavaProcesses(processes []*process.Process) []*DiscoveryCandidate {
	// var javaProcesses []*process.Process
	var javaProcesses []*DiscoveryCandidate

	for _, proc := range processes {
		if discoveryCandidate := d.getJavaDiscoveryCandidateForProcesss(proc); discoveryCandidate.IsJavaProcess {
			javaProcesses = append(javaProcesses, &discoveryCandidate)
		}
	}

	return javaProcesses
}

func (d *discoverer) filterNodeProcesses(processes []*process.Process) []*process.Process {
	var nodeProcesses []*process.Process

	for _, proc := range processes {
		if d.isNodeProcess(proc) {
			nodeProcesses = append(nodeProcesses, proc)
		}
	}

	return nodeProcesses
}

func (d *discoverer) filterPythonProcesses(processes []*process.Process) []*process.Process {
	var pythonProcesses []*process.Process

	for _, proc := range processes {
		if d.isNodeProcess(proc) {
			pythonProcesses = append(pythonProcesses, proc)
		}
	}

	return pythonProcesses
}

func (d *discoverer) isPythonProcess(proc *process.Process) bool {
	// 1. Check executable patterns
	exe, err := proc.Exe()
	if err == nil {
		exeName := strings.ToLower(filepath.Base(exe))

		// Check for standard python names (python, python3, python3.10, etc.)
		if strings.HasPrefix(exeName, "python") || exeName == "pypy" || exeName == "pypy3" {
			return true
		}

		// Check for common python-based entry point binaries
		pyBinaries := []string{"gunicorn", "uvicorn", "celery", "flask", "django-admin"}
		for _, bin := range pyBinaries {
			if exeName == bin {
				return true
			}
		}
	}

	// 2. Check command line for Python patterns
	cmdline, err := proc.Cmdline()
	if err == nil {
		cmdLower := strings.ToLower(cmdline)

		// Look for common execution patterns
		pythonPatterns := []string{
			"python ",
			"python3 ",
			"pip install",
			"gunicorn ",
			"uvicorn ",
			"celery ",
			"manage.py runserver",
			"flask run",
		}
		for _, pattern := range pythonPatterns {
			if strings.Contains(cmdLower, pattern) {
				return true
			}
		}

		// Check for .py files in the command line as a fallback
		if strings.Contains(cmdLower, ".py") {
			return true
		}
	}

	return false
}

func (d *discoverer) isNodeProcess(proc *process.Process) bool {
	// Check executable patterns
	exe, err := proc.Exe()
	if err == nil {
		exeName := strings.ToLower(filepath.Base(exe))
		nodeExecutables := []string{"node", "nodejs"}
		for _, nodeExe := range nodeExecutables {
			if exeName == nodeExe {
				return true
			}
		}
	}

	// Check command line for Node.js patterns
	cmdline, err := proc.Cmdline()
	if err == nil {
		cmdLower := strings.ToLower(cmdline)
		nodePatterns := []string{
			"node ",
			"npm start",
			"npm run",
			"npx ",
			"yarn start",
			"yarn run",
		}
		for _, pattern := range nodePatterns {
			if strings.Contains(cmdLower, pattern) {
				return true
			}
		}
	}

	return false
}

// isJavaProcess checks if a process is a Java process
func (d *discoverer) getJavaDiscoveryCandidateForProcesss(
	proc *process.Process,
) DiscoveryCandidate {
	// Try to get the executable name
	var discoveryCandidate DiscoveryCandidate

	exe, err := proc.Exe()
	if err != nil {
		// If we can't get exe, try cmdline as fallback
		cmdline, err := proc.Cmdline()
		discoveryCandidate.Cmdline = cmdline
		if err != nil {
			discoveryCandidate.IsJavaProcess = false
		}
		discoveryCandidate.IsJavaProcess = strings.Contains(strings.ToLower(cmdline), "java")
	}
	discoveryCandidate.Process = proc
	// Check if executable contains "java"
	exeName := strings.ToLower(strings.TrimSpace(exe))
	discoveryCandidate.IsJavaProcess = strings.Contains(exeName, "java") || strings.HasSuffix(exeName, "/java")
	discoveryCandidate.Exe = exeName

	discoveryCandidate.PPid, err = proc.Ppid()
	if err != nil {
		discoveryCandidate.PPid = -1
	}

	discoveryCandidate.Owner, err = d.getProcessOwner(proc)
	if err != nil {
		discoveryCandidate.Owner = "unknown"
	}

	discoveryCandidate.CreateTime, err = proc.CreateTime()
	if err != nil {
		discoveryCandidate.CreateTime = 0
	}

	discoveryCandidate.Status, err = proc.Status()
	if err != nil {
		discoveryCandidate.Status = []string{"unknown"}
	}

	return discoveryCandidate
}

// processWithWorkerPool processes Java processes concurrently
func (d *discoverer) processWithWorkerPool(
	ctx context.Context,
	processes []*process.Process,
	opts DiscoveryOptions,
) ([]JavaProcess, error) {
	if len(processes) == 0 {
		return []JavaProcess{}, nil
	}

	// Create channels for work distribution
	jobs := make(chan *process.Process, len(processes))
	results := make(chan processResult, len(processes))

	// Start worker goroutines
	numWorkers := opts.MaxConcurrency
	if numWorkers <= 0 {
		numWorkers = 10 // default
	}
	if numWorkers > len(processes) {
		numWorkers = len(processes)
	}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go d.worker(ctx, jobs, results, opts, &wg)
	}

	// Send jobs to workers
	go func() {
		defer close(jobs)
		for _, proc := range processes {
			select {
			case jobs <- proc:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for workers to finish and collect results
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var javaProcesses []JavaProcess
	var errors []error

	for result := range results {
		if result.err != nil {
			if !opts.SkipPermissionErrors {
				errors = append(errors, result.err)
			}
			continue
		}

		if result.process != nil {
			// Apply filters
			if d.passesFilter(*result.process, opts.Filter) {
				javaProcesses = append(javaProcesses, *result.process)
			}
		}
	}

	// Return error if we have errors and not skipping them
	if len(errors) > 0 && !opts.SkipPermissionErrors {
		return javaProcesses, fmt.Errorf("encountered %d errors during discovery: %v", len(errors), errors[0])
	}

	return javaProcesses, nil
}

// nodeProcessResult holds the result of processing a single Node.js process
type nodeProcessResult struct {
	process *NodeProcess
	err     error
}

// processNodeWithWorkerPool processes Node.js processes concurrently
func (d *discoverer) processNodeWithWorkerPool(ctx context.Context, processes []*process.Process, opts DiscoveryOptions) ([]NodeProcess, error) {
	if len(processes) == 0 {
		return []NodeProcess{}, nil
	}

	// Create channels for work distribution
	jobs := make(chan *process.Process, len(processes))
	results := make(chan nodeProcessResult, len(processes))

	// Start worker goroutines
	numWorkers := opts.MaxConcurrency
	if numWorkers <= 0 {
		numWorkers = 10 // default
	}
	if numWorkers > len(processes) {
		numWorkers = len(processes)
	}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go d.nodeWorker(ctx, jobs, results, opts, &wg)
	}

	// Send jobs to workers
	go func() {
		defer close(jobs)
		for _, proc := range processes {
			select {
			case jobs <- proc:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for workers to finish and collect results
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var nodeProcesses []NodeProcess
	var errors []error

	for result := range results {
		if result.err != nil {
			if !opts.SkipPermissionErrors {
				errors = append(errors, result.err)
			}
			continue
		}

		if result.process != nil {
			// Apply filters (you'll need to implement passesNodeFilter)
			if d.passesNodeFilter(*result.process, opts.Filter) {
				nodeProcesses = append(nodeProcesses, *result.process)
			}
		}
	}

	// Return error if we have errors and not skipping them
	if len(errors) > 0 && !opts.SkipPermissionErrors {
		return nodeProcesses, fmt.Errorf("encountered %d errors during Node.js discovery: %v", len(errors), errors[0])
	}
	return nodeProcesses, nil
}

// nodeWorker processes individual Node.js processes
func (d *discoverer) nodeWorker(ctx context.Context, jobs <-chan *process.Process, results chan<- nodeProcessResult, opts DiscoveryOptions, wg *sync.WaitGroup) {
	defer wg.Done()

	for proc := range jobs {
		select {
		case <-ctx.Done():
			results <- nodeProcessResult{nil, ctx.Err()}
			return
		default:
		}

		nodeProc, err := d.processOneNode(ctx, proc, opts)
		results <- nodeProcessResult{nodeProc, err}
	}
}

// processOneNode processes a single Node.js process
func (d *discoverer) processOneNode(ctx context.Context, proc *process.Process, opts DiscoveryOptions) (*NodeProcess, error) {
	// Get basic process information
	pid := proc.Pid

	cmdline, err := proc.Cmdline()
	if err != nil {
		return nil, fmt.Errorf("failed to get cmdline for PID %d: %w", pid, err)
	}

	exe, err := proc.Exe()
	if err != nil {
		// Use fallback if exe is not accessible
		exe = "node"
	}

	// Get parent PID
	parentPID, err := proc.Ppid()
	if err != nil {
		parentPID = 0
	}

	// Get process owner
	owner, err := d.getProcessOwner(proc)
	if err != nil {
		owner = "unknown"
	}

	// Get process create time
	createTime, err := proc.CreateTime()
	if err != nil {
		createTime = 0
	}
	createTimeStamp := time.Unix(createTime/1000, 0)

	// Get process status
	status, err := proc.Status()
	if err != nil {
		status = []string{"unknown"}
	}
	statusStr := strings.Join(status, ",")

	// Parse command line arguments
	cmdArgs := d.parseCommandLine(cmdline)

	// Initialize the Node.js process structure
	nodeProc := &NodeProcess{
		ProcessPID:            pid,
		ProcessParentPID:      parentPID,
		ProcessExecutableName: d.getExecutableName(exe),
		ProcessExecutablePath: exe,
		ProcessCommand:        cmdline,
		ProcessCommandLine:    cmdline,
		ProcessCommandArgs:    cmdArgs,
		ProcessOwner:          owner,
		ProcessCreateTime:     createTimeStamp,
		Status:                statusStr,

		// Node.js runtime information
		ProcessRuntimeName:        "node",
		ProcessRuntimeVersion:     d.extractNodeVersion(cmdArgs),
		ProcessRuntimeDescription: "Node.js Runtime",
	}

	// === CONTAINER DETECTION ===
	if opts.IncludeContainerInfo || opts.ExcludeContainers {
		containerInfo, err := d.containerDetector.IsProcessInContainer(nodeProc.ProcessPID)
		if err != nil {
			fmt.Printf("Warning: Could not detect container info for Node.js PID %d: %v\n", nodeProc.ProcessPID, err)
		} else {
			nodeProc.ContainerInfo = containerInfo

			// If we're excluding containers and this is in a container, return nil
			if opts.ExcludeContainers && containerInfo.IsContainer {
				return nil, fmt.Errorf("Node.js process %d is running in container, skipping", nodeProc.ProcessPID)
			}

			// If container name is available, try to get it
			if containerInfo.IsContainer && containerInfo.ContainerID != "" {
				containerName := d.containerDetector.GetContainerNameByID(
					containerInfo.ContainerID,
					containerInfo.Runtime,
				)
				if containerName != "" {
					nodeProc.ContainerInfo.ContainerName = strings.TrimPrefix(containerName, "/")
				}
			}
		}
	}

	// Extract Node.js-specific information
	d.extractNodeInfo(nodeProc, cmdArgs)

	// Extract service name
	d.extractNodeServiceName(nodeProc, cmdArgs)

	// Detect process manager (PM2, Forever, etc.)
	d.detectProcessManager(nodeProc, cmdArgs)

	// Detect instrumentation
	d.detectNodeInstrumentation(nodeProc, cmdArgs)

	// Get metrics if requested
	// if opts.IncludeMetrics {
	// 	d.addMetrics(proc, &javaProc) // Reuse metrics from Java process
	// 	// Copy metrics to NodeProcess
	// 	nodeProc.MemoryPercent = javaProc.MemoryPercent
	// 	nodeProc.CPUPercent = javaProc.CPUPercent
	// }

	return nodeProc, nil
}

// extractNodeInfo extracts Node.js-specific information from command arguments
func (d *discoverer) extractNodeInfo(nodeProc *NodeProcess, cmdArgs []string) {
	var entryPoint string
	var workingDirectory string

	// Extract working directory from process
	if wd, err := os.Getwd(); err == nil {
		workingDirectory = wd
	}

	// Find the main entry point (script file)
	for i, arg := range cmdArgs {
		// Skip the node executable itself and flags
		if i == 0 || strings.HasPrefix(arg, "-") {
			continue
		}

		// Look for .js files or entry points
		if strings.HasSuffix(arg, ".js") || strings.HasSuffix(arg, ".mjs") || strings.HasSuffix(arg, ".ts") {
			entryPoint = arg

			// Try to get absolute path and working directory
			if !filepath.IsAbs(entryPoint) {
				if absPath, err := filepath.Abs(entryPoint); err == nil {
					workingDirectory = filepath.Dir(absPath)
					entryPoint = filepath.Base(absPath)
				}
			} else {
				workingDirectory = filepath.Dir(entryPoint)
				entryPoint = filepath.Base(entryPoint)
			}
			break
		}
	}

	nodeProc.EntryPoint = entryPoint
	nodeProc.WorkingDirectory = workingDirectory

	// Try to find and parse package.json
	if workingDirectory != "" {
		d.extractPackageInfo(nodeProc, workingDirectory)
	}
}

// extractPackageInfo attempts to extract information from package.json
func (d *discoverer) extractPackageInfo(nodeProc *NodeProcess, workingDir string) {
	packageJsonPath := filepath.Join(workingDir, "package.json")
	nodeProc.PackageJsonPath = packageJsonPath

	// For now, just check if package.json exists
	// In a full implementation, you'd parse the JSON file
	if _, err := os.Stat(packageJsonPath); err == nil {
		// TODO: Parse package.json and extract name, version, dependencies
		// This would require json.Unmarshal(data, &PackageInfo{})
		nodeProc.PackageName = "unknown"    // Placeholder
		nodeProc.PackageVersion = "unknown" // Placeholder
	}
}

// extractNodeServiceName extracts a meaningful service name from Node.js process
func (d *discoverer) extractNodeServiceName(nodeProc *NodeProcess, cmdArgs []string) {
	serviceName := ""

	// Strategy 1: Environment variables (NODE_ENV, SERVICE_NAME, etc.)
	serviceName = d.extractFromNodeEnvironment(cmdArgs)
	if serviceName != "" {
		nodeProc.ServiceName = serviceName
		return
	}

	// Strategy 2: Package name from package.json
	if nodeProc.PackageName != "" && nodeProc.PackageName != "unknown" {
		serviceName = d.cleanServiceName(nodeProc.PackageName)
		if serviceName != "" {
			nodeProc.ServiceName = serviceName
			return
		}
	}

	// Strategy 3: Entry point file name
	if nodeProc.EntryPoint != "" {
		serviceName = d.extractFromNodeScript(nodeProc.EntryPoint)
		if serviceName != "" {
			nodeProc.ServiceName = serviceName
			return
		}
	}

	// Strategy 4: Working directory name
	if nodeProc.WorkingDirectory != "" {
		serviceName = d.extractFromDirectory(nodeProc.WorkingDirectory)
		if serviceName != "" {
			nodeProc.ServiceName = serviceName
			return
		}
	}

	// Final fallback
	nodeProc.ServiceName = "node-service"
}

// extractFromNodeEnvironment looks for service name in Node.js environment variables
func (d *discoverer) extractFromNodeEnvironment(cmdArgs []string) string {
	// Look for common Node.js service name patterns in environment or arguments
	serviceProperties := []string{
		"--name=",
		"--service=",
		"SERVICE_NAME=",
		"NODE_ENV=",
	}

	for _, arg := range cmdArgs {
		for _, prop := range serviceProperties {
			if strings.Contains(arg, prop) {
				serviceName := strings.TrimPrefix(arg, prop)
				serviceName = strings.Trim(serviceName, `"'`)
				if serviceName != "" && serviceName != "production" && serviceName != "development" {
					return d.cleanServiceName(serviceName)
				}
			}
		}
	}

	return ""
}

// extractFromNodeScript extracts service name from Node.js script file name
func (d *discoverer) extractFromNodeScript(scriptName string) string {
	if scriptName == "" {
		return ""
	}

	// Remove extension and path
	baseName := strings.TrimSuffix(scriptName, filepath.Ext(scriptName))
	baseName = filepath.Base(baseName)

	// Skip generic names
	if d.isGenericNodeName(baseName) {
		return ""
	}

	return d.cleanServiceName(baseName)
}

// isGenericNodeName checks if a name is too generic for Node.js services
func (d *discoverer) isGenericNodeName(name string) bool {
	genericNames := []string{
		"index", "app", "main", "server", "start", "run",
		"node", "npm", "yarn", "nodemon", "pm2", "forever",
	}

	nameLower := strings.ToLower(name)
	for _, generic := range genericNames {
		if nameLower == generic {
			return true
		}
	}

	return false
}

// detectProcessManager detects if the Node.js process is managed by PM2, Forever, etc.
func (d *discoverer) detectProcessManager(nodeProc *NodeProcess, cmdArgs []string) {
	cmdline := strings.Join(cmdArgs, " ")
	cmdlineLower := strings.ToLower(cmdline)

	// Detect PM2
	if strings.Contains(cmdlineLower, "pm2") || strings.Contains(cmdlineLower, "/pm2/") {
		nodeProc.IsPM2Process = true
		nodeProc.ProcessManager = "pm2"

		// Try to extract PM2 process name
		for _, arg := range cmdArgs {
			if strings.HasPrefix(arg, "--name=") {
				nodeProc.PM2ProcessName = strings.TrimPrefix(arg, "--name=")
				break
			}
		}
	}

	// Detect Forever
	if strings.Contains(cmdlineLower, "forever") || strings.Contains(cmdlineLower, "/forever/") {
		nodeProc.IsForeverProcess = true
		nodeProc.ProcessManager = "forever"
	}

	// Detect systemd (heuristic)
	if nodeProc.ProcessManager == "" && nodeProc.ProcessOwner != "root" && nodeProc.ProcessOwner != "node" {
		nodeProc.ProcessManager = "systemd"
	}
}

// detectNodeInstrumentation detects Node.js agents and instrumentation
func (d *discoverer) detectNodeInstrumentation(nodeProc *NodeProcess, cmdArgs []string) {
	// Reset instrumentation flags
	nodeProc.HasNodeAgent = false
	nodeProc.IsMiddlewareAgent = false
	nodeProc.NodeAgentPath = ""

	// Look for Node.js agent patterns in command line
	cmdline := strings.Join(cmdArgs, " ")

	// Common Node.js instrumentation patterns
	if d.detectNodeAgentInCmdline(nodeProc, cmdline) {
		return
	}

	// Check environment variables for NODE_OPTIONS or other agent indicators
	d.checkNodeEnvironmentForAgent(nodeProc)
}

// detectNodeAgentInCmdline detects Node.js agents in command line
func (d *discoverer) detectNodeAgentInCmdline(nodeProc *NodeProcess, cmdline string) bool {
	// Look for --require or --import flags
	agentPatterns := []string{
		"--require ",
		"-r ",
		"--import ",
		"--loader ",
	}

	cmdlineLower := strings.ToLower(cmdline)
	for _, pattern := range agentPatterns {
		if strings.Contains(cmdlineLower, pattern) {
			// Try to extract agent path
			agentPath := d.extractNodeAgentPath(cmdline, pattern)
			if agentPath != "" {
				d.setNodeAgentInfo(nodeProc, agentPath)
				return true
			}
		}
	}

	return false
}

// extractNodeAgentPath extracts the Node.js agent path from command line
func (d *discoverer) extractNodeAgentPath(cmdline, pattern string) string {
	idx := strings.Index(strings.ToLower(cmdline), pattern)
	if idx == -1 {
		return ""
	}

	// Get the part after the pattern
	agentPart := cmdline[idx+len(pattern):]

	// Extract until the next space or end of string
	if spaceIdx := strings.Index(agentPart, " "); spaceIdx != -1 {
		agentPart = agentPart[:spaceIdx]
	}

	return strings.TrimSpace(agentPart)
}

// setNodeAgentInfo sets agent information for a Node.js process
func (d *discoverer) setNodeAgentInfo(nodeProc *NodeProcess, agentPath string) {
	nodeProc.HasNodeAgent = true
	nodeProc.NodeAgentPath = agentPath

	// Detect agent type
	if d.isMiddlewareNodeAgent(agentPath) {
		nodeProc.IsMiddlewareAgent = true
	}
}

// isMiddlewareNodeAgent checks if the agent path indicates a Middleware Node.js agent
func (d *discoverer) isMiddlewareNodeAgent(agentPath string) bool {
	agentPathLower := strings.ToLower(agentPath)
	middlewarePatterns := []string{
		"middleware",
		"mw-",
		"mw.js",
		"middleware-agent",
		"mw-register",
	}

	for _, pattern := range middlewarePatterns {
		if strings.Contains(agentPathLower, pattern) {
			return true
		}
	}
	return false
}

// checkNodeEnvironmentForAgent checks environment variables for Node.js agents
func (d *discoverer) checkNodeEnvironmentForAgent(nodeProc *NodeProcess) {
	// Read /proc/PID/environ
	environPath := fmt.Sprintf("/proc/%d/environ", nodeProc.ProcessPID)
	data, err := os.ReadFile(environPath)
	if err != nil {
		return // Permission denied or process gone
	}

	// Parse environment variables (null-terminated strings)
	envVars := strings.Split(string(data), "\x00")

	for _, env := range envVars {
		// Check NODE_OPTIONS
		if strings.HasPrefix(env, "NODE_OPTIONS=") {
			value := strings.TrimPrefix(env, "NODE_OPTIONS=")
			if agentPath := d.extractNodeAgentFromEnv(value); agentPath != "" {
				d.setNodeAgentInfo(nodeProc, agentPath)
				return
			}
		}
	}
}

// extractNodeAgentFromEnv extracts Node.js agent path from environment variable value
func (d *discoverer) extractNodeAgentFromEnv(envValue string) string {
	// Look for --require or --import in NODE_OPTIONS
	agentPatterns := []string{"--require ", "-r ", "--import ", "--loader "}

	for _, pattern := range agentPatterns {
		if idx := strings.Index(envValue, pattern); idx != -1 {
			agentPart := envValue[idx+len(pattern):]
			if spaceIdx := strings.Index(agentPart, " "); spaceIdx != -1 {
				agentPart = agentPart[:spaceIdx]
			}
			return agentPart
		}
	}
	return ""
}

// extractNodeVersion attempts to extract Node.js version
func (d *discoverer) extractNodeVersion(args []string) string {
	// You could run `node --version` or extract from process info
	// For now, return unknown
	return "unknown"
}

// passesNodeFilter checks if a Node.js process passes the given filter criteria
func (d *discoverer) passesNodeFilter(proc NodeProcess, filter ProcessFilter) bool {
	// Reuse the same filter logic as Java processes
	// You can customize this for Node.js-specific filtering if needed

	// Check user filters
	if filter.CurrentUserOnly {
		currentUser := os.Getenv("USER")
		if proc.ProcessOwner != currentUser {
			return false
		}
	}

	// Add more Node.js-specific filters as needed

	return true
}

// processResult holds the result of processing a single process
type processResult struct {
	process *JavaProcess
	err     error
}

// worker processes individual processes
func (d *discoverer) worker(
	ctx context.Context,
	jobs <-chan *process.Process,
	results chan<- processResult,
	opts DiscoveryOptions,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	for proc := range jobs {
		select {
		case <-ctx.Done():
			results <- processResult{nil, ctx.Err()}
			return
		default:
		}

		candidate := d.getJavaDiscoveryCandidateForProcesss(proc)
		if !candidate.IsJavaProcess {
			continue
		}

		javaProc, err := d.processOne(ctx, &candidate, opts)
		results <- processResult{javaProc, err}
	}
}

// processOne processes a single Java process
func (d *discoverer) processOne(ctx context.Context, proc *DiscoveryCandidate, opts DiscoveryOptions) (*JavaProcess, error) {
	// Get basic process information
	if !proc.IsJavaProcess {
		return nil, nil
	}
	cmdline, err := proc.Process.Cmdline()
	if err != nil {
		return nil, fmt.Errorf("failed to get cmdline for PID %d: %w", proc.Process.Pid, err)
	}
	cmdArgs := d.parseCommandLine(cmdline)

	// Initialize the Java process structure
	javaProc := &JavaProcess{
		ProcessPID:       proc.Process.Pid,
		ProcessParentPID: proc.PPid,

		ProcessExecutableName: d.getExecutableName(proc.Exe),
		ProcessExecutablePath: proc.Exe,
		ProcessCommand:        cmdline,
		ProcessCommandLine:    cmdline,
		ProcessCommandArgs:    cmdArgs,
		ProcessOwner:          proc.Owner,
		ProcessCreateTime:     time.Unix(proc.CreateTime/1000, 0),
		Status:                strings.Join(proc.Status, ","),

		// Java runtime information
		ProcessRuntimeName:        "java",
		ProcessRuntimeVersion:     d.extractJavaVersion(cmdArgs),
		ProcessRuntimeDescription: "Java Virtual Machine",
	}

	// === CONTAINER DETECTION ===
	if opts.IncludeContainerInfo || opts.ExcludeContainers {
		containerInfo, err := d.containerDetector.IsProcessInContainer(javaProc.ProcessPID)
		if err != nil {
			// Log error but don't fail discovery
			// In production, you might want to use a proper logger
			fmt.Printf("Warning: Could not detect container info for PID %d: %v\n", javaProc.ProcessPID, err)
		} else {
			javaProc.ContainerInfo = containerInfo

			// If we're excluding containers and this is in a container, return nil
			if opts.ExcludeContainers && containerInfo.IsContainer {
				return nil, fmt.Errorf("process %d is running in container, skipping", javaProc.ProcessPID)
			}

			// If container name is available, try to get it
			if containerInfo.IsContainer && containerInfo.ContainerID != "" {
				containerName := d.containerDetector.GetContainerNameByID(
					containerInfo.ContainerID,
					containerInfo.Runtime,
				)
				if containerName != "" {
					javaProc.ContainerInfo.ContainerName = strings.TrimPrefix(containerName, "/")
				}
			}
		}
	}

	// Extract Java-specific information
	d.extractJavaInfo(javaProc, cmdArgs)

	// Extract service name
	d.extractServiceName(javaProc, cmdArgs)

	tomcatInfo := d.detectTomcatDeployment(javaProc, cmdArgs)
	if tomcatInfo.IsTomcat {
		// Override service name for Tomcat if not already set by properties
		if javaProc.ServiceName == "" || javaProc.ServiceName == "java-service" {
			if tomcatInfo.InstanceName != "" {
				javaProc.ServiceName = tomcatInfo.InstanceName
			} else {
				javaProc.ServiceName = "tomcat"
			}
		}
	}
	// Detect instrumentation
	d.detectInstrumentation(javaProc, cmdArgs)

	// Get metrics if requested
	if opts.IncludeMetrics {
		d.addMetrics(proc.Process, javaProc)
	}

	return javaProc, nil
}

// getProcessOwner gets the owner of the process
// 2. Update getProcessOwner to use the cache
func (d *discoverer) getProcessOwner(proc *process.Process) (string, error) {
	uids, err := proc.Uids()
	if err != nil || len(uids) == 0 {
		return "", err
	}

	uid := fmt.Sprintf("%d", uids[0])

	// Check cache first to avoid syscalls/network lookups
	if cachedName, ok := d.userCache.Load(uid); ok {
		return cachedName.(string), nil
	}

	u, err := user.LookupId(uid)
	if err != nil {
		return uid, nil
	}

	d.userCache.Store(uid, u.Username)
	return u.Username, nil
}

// getExecutableName extracts the executable name from path
func (d *discoverer) getExecutableName(exePath string) string {
	parts := strings.Split(exePath, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return exePath
}

// parseCommandLine parses the command line into arguments
func (d *discoverer) parseCommandLine(cmdline string) []string {
	// Simple split by spaces - could be enhanced for quoted arguments
	return strings.Fields(cmdline)
}

// extractJavaVersion attempts to extract Java version from command args
func (d *discoverer) extractJavaVersion(args []string) string {
	// Look for -version flag or try to infer from java executable
	for i, arg := range args {
		if arg == "-version" && i > 0 {
			// This is a version check command, not a running service
			return "unknown"
		}
	}

	// Could enhance this to actually detect Java version
	return "unknown"
}

// addMetrics adds CPU and memory metrics to the process
func (d *discoverer) addMetrics(proc *process.Process, javaProc *JavaProcess) {
	// Get memory percentage
	if memPercent, err := proc.MemoryPercent(); err == nil {
		javaProc.MemoryPercent = memPercent
	}

	// Get CPU percentage
	// if cpuPercent, err := proc.CPUPercent(); err == nil {
	// 	javaProc.CPUPercent = cpuPercent
	// }
}

// passesFilter checks if a process passes the given filter criteria
func (d *discoverer) passesFilter(proc JavaProcess, filter ProcessFilter) bool {
	// Check user filters
	if filter.CurrentUserOnly {
		currentUser, err := user.Current()
		if err != nil || proc.ProcessOwner != currentUser.Username {
			return false
		}
	}

	if len(filter.IncludeUsers) > 0 {
		found := false
		for _, u := range filter.IncludeUsers {
			if proc.ProcessOwner == u {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(filter.ExcludeUsers) > 0 {
		for _, u := range filter.ExcludeUsers {
			if proc.ProcessOwner == u {
				return false
			}
		}
	}

	// Check agent filters
	if filter.HasJavaAgentOnly && !proc.HasJavaAgent {
		return false
	}

	if filter.HasMWAgentOnly && !proc.IsMiddlewareAgent {
		return false
	}

	// Check service name pattern
	if filter.ServiceNamePattern != "" {
		matched, err := regexp.MatchString(filter.ServiceNamePattern, proc.ServiceName)
		if err != nil || !matched {
			return false
		}
	}

	// Check memory filter
	if filter.MinMemoryMB > 0 {
		// Convert memory percentage to approximate MB (this is a rough calculation)
		// In a production system, you'd want more accurate memory calculation
		if proc.MemoryPercent < filter.MinMemoryMB {
			return false
		}
	}

	return true
}

// FindContainerJavaProcesses finds only Java processes running in containers
func FindContainerJavaProcesses(ctx context.Context) ([]JavaProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true

	discoverer := NewDiscovererWithOptions(ctx, opts)
	defer discoverer.Close()

	allProcesses, err := discoverer.DiscoverWithOptions(ctx, opts)
	if err != nil {
		return nil, err
	}

	// Filter to only return container processes
	var containerProcesses []JavaProcess
	for _, proc := range allProcesses {
		if proc.IsInContainer() {
			containerProcesses = append(containerProcesses, proc)
		}
	}

	return containerProcesses, nil
}
