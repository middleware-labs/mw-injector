package discovery

import (
	"context"
	"fmt"
	"os/user"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// discoverer implements the Discoverer interface
type discoverer struct {
	ctx               context.Context
	opts              DiscoveryOptions
	containerDetector *ContainerDetector
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
	javaProcesses := d.filterJavaProcesses(allProcesses)
	// Process concurrently with worker pool
	return d.processWithWorkerPool(ctx, javaProcesses, opts)
}

// RefreshProcess updates information for a specific process
func (d *discoverer) RefreshProcess(ctx context.Context, pid int32) (*JavaProcess, error) {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("process %d not found: %w", pid, err)
	}

	// Check if it's a Java process
	if !d.isJavaProcess(proc) {
		return nil, fmt.Errorf("process %d is not a Java process", pid)
	}

	javaProc, err := d.processOne(ctx, proc, d.opts)
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
func (d *discoverer) filterJavaProcesses(processes []*process.Process) []*process.Process {
	var javaProcesses []*process.Process

	for _, proc := range processes {
		if d.isJavaProcess(proc) {
			javaProcesses = append(javaProcesses, proc)
		}
	}

	return javaProcesses
}

// isJavaProcess checks if a process is a Java process
func (d *discoverer) isJavaProcess(proc *process.Process) bool {
	// Try to get the executable name
	exe, err := proc.Exe()
	if err != nil {
		// If we can't get exe, try cmdline as fallback
		cmdline, err := proc.Cmdline()
		if err != nil {
			return false
		}
		return strings.Contains(strings.ToLower(cmdline), "java")
	}

	// Check if executable contains "java"
	exeName := strings.ToLower(strings.TrimSpace(exe))
	return strings.Contains(exeName, "java") || strings.HasSuffix(exeName, "/java")
}

// processWithWorkerPool processes Java processes concurrently
func (d *discoverer) processWithWorkerPool(ctx context.Context, processes []*process.Process, opts DiscoveryOptions) ([]JavaProcess, error) {
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

// processResult holds the result of processing a single process
type processResult struct {
	process *JavaProcess
	err     error
}

// worker processes individual processes
func (d *discoverer) worker(ctx context.Context, jobs <-chan *process.Process, results chan<- processResult, opts DiscoveryOptions, wg *sync.WaitGroup) {
	defer wg.Done()

	for proc := range jobs {
		select {
		case <-ctx.Done():
			results <- processResult{nil, ctx.Err()}
			return
		default:
		}

		javaProc, err := d.processOne(ctx, proc, opts)
		results <- processResult{javaProc, err}
	}
}

// processOne processes a single Java process
func (d *discoverer) processOne(ctx context.Context, proc *process.Process, opts DiscoveryOptions) (*JavaProcess, error) {
	// Get basic process information
	pid := proc.Pid

	cmdline, err := proc.Cmdline()
	if err != nil {
		return nil, fmt.Errorf("failed to get cmdline for PID %d: %w", pid, err)
	}

	exe, err := proc.Exe()
	if err != nil {
		// Use fallback if exe is not accessible
		exe = "java"
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

	// Initialize the Java process structure
	javaProc := &JavaProcess{
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
		d.addMetrics(proc, javaProc)
	}

	return javaProc, nil
}

// getProcessOwner gets the owner of the process
func (d *discoverer) getProcessOwner(proc *process.Process) (string, error) {
	uids, err := proc.Uids()
	if err != nil || len(uids) == 0 {
		return "", err
	}

	uid := fmt.Sprintf("%d", uids[0])
	u, err := user.LookupId(uid)
	if err != nil {
		return uid, nil // Return UID if we can't resolve username
	}

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
	if cpuPercent, err := proc.CPUPercent(); err == nil {
		javaProc.CPUPercent = cpuPercent
	}
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
