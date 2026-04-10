// Package discovery provides process discovery and instrumentation detection
// capabilities following OpenTelemetry semantic conventions.
package discovery

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// JavaProcess represents a discovered Java process with OTEL semantic convention compliance
type JavaProcess struct {
	// OTEL Process semantic conventions
	ProcessPID            int32     `json:"process.pid"`
	ProcessParentPID      int32     `json:"process.parent_pid"`
	ProcessExecutableName string    `json:"process.executable.name"`
	ProcessExecutablePath string    `json:"process.executable.path"`
	ProcessCommand        string    `json:"process.command"`
	ProcessCommandLine    string    `json:"process.command_line"`
	ProcessCommandArgs    []string  `json:"process.command_args"`
	ProcessOwner          string    `json:"process.owner"`
	ProcessCreateTime     time.Time `json:"process.create_time"`

	// OTEL Process Runtime semantic conventions
	ProcessRuntimeName        string `json:"process.runtime.name"`
	ProcessRuntimeVersion     string `json:"process.runtime.version"`
	ProcessRuntimeDescription string `json:"process.runtime.description"`

	// Java-specific information
	JarFile    string   `json:"java.jar.file,omitempty"`
	JarPath    string   `json:"java.jar.path,omitempty"`
	MainClass  string   `json:"java.main.class,omitempty"`
	JVMOptions []string `json:"java.jvm.options,omitempty"`

	// Instrumentation detection
	HasJavaAgent      bool   `json:"java.agent.present"`
	JavaAgentPath     string `json:"java.agent.path,omitempty"`
	JavaAgentName     string `json:"java.agent.name,omitempty"`
	IsMiddlewareAgent bool   `json:"middleware.agent.detected"`

	// Service identification
	ServiceName string `json:"service.name,omitempty"`

	// Process metrics
	MemoryPercent float32 `json:"process.memory.percent"`
	CPUPercent    float64 `json:"process.cpu.percent"`
	Status        string  `json:"process.status"`

	ContainerInfo *ContainerInfo `json:"container_info,omitempty"`
}

func (jp *JavaProcess) IsInContainer() bool {
	return jp.ContainerInfo != nil && jp.ContainerInfo.IsContainer
}

func (jp *JavaProcess) GetContainerRuntime() string {
	if jp.ContainerInfo != nil {
		return jp.ContainerInfo.Runtime
	}
	return ""
}

func (jp *JavaProcess) GetContainerID() string {
	if jp.ContainerInfo != nil {
		return jp.ContainerInfo.ContainerID
	}
	return ""
}

func (jp *JavaProcess) GetContainerName() string {
	if jp.ContainerInfo != nil {
		return jp.ContainerInfo.ContainerName
	}
	return ""
}

// DiscoveryOptions configures the discovery behavior
type DiscoveryOptions struct {
	MaxConcurrency       int           `json:"max_concurrency"`
	Timeout              time.Duration `json:"timeout"`
	Filter               ProcessFilter `json:"filter"`
	ExcludeContainers    bool          `json:"exclude_containers"`
	IncludeContainerInfo bool          `json:"include_container_info"`
}

// ProcessFilter defines filtering criteria for process discovery
type ProcessFilter struct {
	IncludeUsers     []string `json:"include_users,omitempty"`
	ExcludeUsers     []string `json:"exclude_users,omitempty"`
	CurrentUserOnly  bool     `json:"current_user_only"`
	HasJavaAgentOnly bool     `json:"has_java_agent_only"`
	HasMWAgentOnly   bool     `json:"has_mw_agent_only"`
}

// AgentType represents the type of Java agent detected
type AgentType int

const (
	AgentNone AgentType = iota
	AgentOpenTelemetry
	AgentMiddleware
	AgentOther
)

// AgentInfo contains details about a detected Java agent
type AgentInfo struct {
	Type         AgentType `json:"type"`
	Path         string    `json:"path"`
	Name         string    `json:"name"`
	Version      string    `json:"version,omitempty"`
	IsServerless bool      `json:"is_serverless,omitempty"`
}

func (a AgentType) String() string {
	switch a {
	case AgentNone:
		return "none"
	case AgentOpenTelemetry:
		return "opentelemetry"
	case AgentMiddleware:
		return "middleware"
	case AgentOther:
		return "other"
	default:
		return "unknown"
	}
}

// DefaultDiscoveryOptions returns sensible defaults for process discovery
func DefaultDiscoveryOptions() DiscoveryOptions {
	return DiscoveryOptions{
		MaxConcurrency: 10,
		Timeout:        30 * time.Second,
		Filter:         ProcessFilter{},
	}
}

// --- discoverer (new implementation using scanner + inspectors) ---

type discoverer struct {
	ctx               context.Context
	opts              DiscoveryOptions
	containerDetector *ContainerDetector
	langRegistry      *LanguageRegistry
	allProcesses      []ProcessInfo // scanned once via ScanProcesses()
}

// NewDiscoverer creates a new process discoverer with default options.
func NewDiscoverer(ctx context.Context, opts DiscoveryOptions) (*discoverer, error) {
	return NewDiscovererWithOptions(ctx, DefaultDiscoveryOptions())
}

// NewDiscovererWithOptions creates a new process discoverer with custom options.
// /proc is read once here; the resulting []ProcessInfo is shared across all
// Discover* methods.
func NewDiscovererWithOptions(ctx context.Context, opts DiscoveryOptions) (*discoverer, error) {
	allProcs := ScanProcesses()
	if allProcs == nil {
		return nil, fmt.Errorf("failed to scan /proc")
	}

	return &discoverer{
		ctx:               ctx,
		opts:              opts,
		containerDetector: NewContainerDetector(),
		langRegistry:      NewLanguageRegistry(),
		allProcesses:      allProcs,
	}, nil
}

func (d *discoverer) Close() error {
	return nil
}

// --- Single-pass classification ---

type classifiedProcess struct {
	info *ProcessInfo
	lang Language
}

// classifyAll runs every scanned process through the language registry and
// returns them grouped by language. This is the "Phase 2" of the pipeline.
func (d *discoverer) classifyAll() map[Language][]ProcessInfo {
	grouped := make(map[Language][]ProcessInfo)
	for i := range d.allProcesses {
		if lang, ok := d.langRegistry.Detect(&d.allProcesses[i]); ok {
			grouped[lang] = append(grouped[lang], d.allProcesses[i])
		}
	}
	return grouped
}

// --- Java discovery ---

func (d *discoverer) DiscoverJavaProcesses(ctx context.Context) ([]JavaProcess, error) {
	return d.DiscoverWithOptions(ctx, d.opts)
}

func (d *discoverer) DiscoverWithOptions(ctx context.Context, opts DiscoveryOptions) ([]JavaProcess, error) {
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Classify all processes, then take only Java
	grouped := d.classifyAll()
	javaInfos := grouped[LangJava]

	return d.enrichJavaWithWorkerPool(ctx, javaInfos, opts)
}

func (d *discoverer) enrichJavaWithWorkerPool(ctx context.Context, infos []ProcessInfo, opts DiscoveryOptions) ([]JavaProcess, error) {
	if len(infos) == 0 {
		return []JavaProcess{}, nil
	}

	type result struct {
		proc *JavaProcess
		err  error
	}

	jobs := make(chan *ProcessInfo, len(infos))
	results := make(chan result, len(infos))

	numWorkers := workerCount(opts.MaxConcurrency, len(infos))

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for info := range jobs {
				select {
				case <-ctx.Done():
					results <- result{nil, ctx.Err()}
					return
				default:
				}
				jp := enrichJavaProcess(info, opts, d.containerDetector)
				results <- result{jp, nil}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i := range infos {
			select {
			case jobs <- &infos[i]:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var javaProcesses []JavaProcess
	for r := range results {
		if r.err != nil {
			continue
		}
		if r.proc != nil {
			if passesJavaFilter(*r.proc, opts.Filter) {
				javaProcesses = append(javaProcesses, *r.proc)
			}
		}
	}

	return javaProcesses, nil
}

// --- Node discovery ---

func (d *discoverer) DiscoverNodeWithOptions(ctx context.Context, opts DiscoveryOptions) ([]NodeProcess, error) {
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	grouped := d.classifyAll()
	nodeInfos := grouped[LangNode]

	return d.enrichNodeWithWorkerPool(ctx, nodeInfos, opts)
}

func (d *discoverer) enrichNodeWithWorkerPool(ctx context.Context, infos []ProcessInfo, opts DiscoveryOptions) ([]NodeProcess, error) {
	if len(infos) == 0 {
		return []NodeProcess{}, nil
	}

	type result struct {
		proc *NodeProcess
		err  error
	}

	jobs := make(chan *ProcessInfo, len(infos))
	results := make(chan result, len(infos))

	numWorkers := workerCount(opts.MaxConcurrency, len(infos))

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for info := range jobs {
				select {
				case <-ctx.Done():
					results <- result{nil, ctx.Err()}
					return
				default:
				}
				np := enrichNodeProcess(info, opts, d.containerDetector)
				results <- result{np, nil}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i := range infos {
			select {
			case jobs <- &infos[i]:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var nodeProcesses []NodeProcess
	for r := range results {
		if r.err != nil {
			continue
		}
		if r.proc != nil {
			if passesSimpleOwnerFilter(r.proc.ProcessOwner, opts.Filter) {
				nodeProcesses = append(nodeProcesses, *r.proc)
			}
		}
	}

	return nodeProcesses, nil
}

// --- Python discovery ---

func (d *discoverer) DiscoverPythonWithOptions(ctx context.Context, opts DiscoveryOptions) ([]PythonProcess, error) {
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	grouped := d.classifyAll()
	pyInfos := grouped[LangPython]

	return d.enrichPythonWithWorkerPool(ctx, pyInfos, opts)
}

func (d *discoverer) enrichPythonWithWorkerPool(ctx context.Context, infos []ProcessInfo, opts DiscoveryOptions) ([]PythonProcess, error) {
	if len(infos) == 0 {
		return []PythonProcess{}, nil
	}

	type result struct {
		proc *PythonProcess
		err  error
	}

	jobs := make(chan *ProcessInfo, len(infos))
	results := make(chan result, len(infos))

	numWorkers := workerCount(opts.MaxConcurrency, len(infos))

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for info := range jobs {
				select {
				case <-ctx.Done():
					results <- result{nil, ctx.Err()}
					return
				default:
				}
				pp := enrichPythonProcess(info, opts, d.containerDetector)
				results <- result{pp, nil}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i := range infos {
			select {
			case jobs <- &infos[i]:
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
	for r := range results {
		if r.err != nil {
			continue
		}
		if r.proc != nil {
			if passesSimpleOwnerFilter(r.proc.ProcessOwner, opts.Filter) {
				pyProcesses = append(pyProcesses, *r.proc)
			}
		}
	}

	return pyProcesses, nil
}

// --- RefreshProcess (preserved for interface compatibility) ---

func (d *discoverer) RefreshProcess(ctx context.Context, pid int32) (*JavaProcess, error) {
	info, ok := readProcDetails(pid)
	if !ok {
		return nil, fmt.Errorf("process %d not found", pid)
	}

	if lang, ok := d.langRegistry.Detect(&info); !ok || lang != LangJava {
		return nil, fmt.Errorf("process %d is not a Java process", pid)
	}

	jp := enrichJavaProcess(&info, d.opts, d.containerDetector)
	if jp == nil {
		return nil, fmt.Errorf("failed to enrich PID %d", pid)
	}

	return jp, nil
}

// --- Public convenience functions (preserved signatures) ---

func FindAllJavaProcesses(ctx context.Context) ([]JavaProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true

	d, err := NewDiscoverer(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("error in finding all java processes: %w", err)
	}
	defer d.Close()

	return d.DiscoverWithOptions(ctx, opts)
}

func FindAllNodeProcesses(ctx context.Context) ([]NodeProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true

	d, err := NewDiscoverer(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("error in finding all node processes: %w", err)
	}
	defer d.Close()

	return d.DiscoverNodeWithOptions(ctx, opts)
}

func FindAllPythonProcess(ctx context.Context) ([]PythonProcess, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true

	d, err := NewDiscoverer(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("error in finding all python processes: %w", err)
	}
	defer d.Close()

	return d.DiscoverPythonWithOptions(ctx, opts)
}


// --- Helpers ---

func workerCount(maxConcurrency, total int) int {
	n := maxConcurrency
	if n <= 0 {
		n = 10
	}
	if n > total {
		n = total
	}
	return n
}

func passesJavaFilter(proc JavaProcess, filter ProcessFilter) bool {
	if filter.CurrentUserOnly {
		// Use readProcessOwner of current process (pid 0 → self)
		// but simpler: compare with $USER
		if proc.ProcessOwner != currentUser() {
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

	if filter.HasJavaAgentOnly && !proc.HasJavaAgent {
		return false
	}
	if filter.HasMWAgentOnly && !proc.IsMiddlewareAgent {
		return false
	}

	return true
}

func passesSimpleOwnerFilter(owner string, filter ProcessFilter) bool {
	if filter.CurrentUserOnly {
		return owner == currentUser()
	}
	return true
}

func currentUser() string {
	return os.Getenv("USER")
}
