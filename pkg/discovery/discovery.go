// Package discovery provides process discovery and instrumentation detection
// for Java, Node.js, and Python processes on Linux hosts.
//
// The discovery pipeline works in four phases:
//  1. Scan: Read /proc to enumerate running processes (scanner.go)
//  2. Classify: Match each process to a language via HandlerRegistry (registry.go)
//  3. Enrich: Populate Process structs with language-specific metadata (java/node/python_handler.go)
//  4. Filter: Apply user criteria (owner, agent presence) to select relevant processes
//
// Adding a new language requires implementing LanguageHandler and registering it
// in NewHandlerRegistry — no changes to the core pipeline.
package discovery

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// DiscoveryOptions configures the discovery behavior.
type DiscoveryOptions struct {
	MaxConcurrency       int           `json:"max_concurrency"`
	Timeout              time.Duration `json:"timeout"`
	Filter               ProcessFilter `json:"filter"`
	ExcludeContainers    bool          `json:"exclude_containers"`
	IncludeContainerInfo bool          `json:"include_container_info"`

	// Logger is an optional structured logger for discovery-phase timing
	// and diagnostics. Nil means no logs. Callers with a zap logger can
	// bridge via `slog.New(zapslog.NewHandler(zapCore, nil))`.
	Logger *slog.Logger `json:"-"`
}

// discardLogger is returned by discoverer.logger() when no logger is set
// so call sites can unconditionally call d.logger().Debug(...) without
// nil checks. Writing to io.Discard is the cheapest available sink.
var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// ProcessFilter defines filtering criteria for process discovery.
type ProcessFilter struct {
	IncludeUsers     []string `json:"include_users,omitempty"`
	ExcludeUsers     []string `json:"exclude_users,omitempty"`
	CurrentUserOnly  bool     `json:"current_user_only"`
	HasJavaAgentOnly bool     `json:"has_java_agent_only"`
	HasMWAgentOnly   bool     `json:"has_mw_agent_only"`
}

// AgentType represents the type of agent detected on a process.
type AgentType int

const (
	AgentNone AgentType = iota
	AgentOpenTelemetry
	AgentMiddleware
	AgentOther
)

// AgentInfo contains details about a detected agent.
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

// DefaultDiscoveryOptions returns sensible defaults for process discovery.
func DefaultDiscoveryOptions() DiscoveryOptions {
	return DiscoveryOptions{
		MaxConcurrency: 10,
		Timeout:        30 * time.Second,
		Filter:         ProcessFilter{},
	}
}

// --- discoverer ---

type discoverer struct {
	ctx               context.Context
	opts              DiscoveryOptions
	containerDetector *ContainerDetector
	containerClients  []ContainerClient
	handlerRegistry   *HandlerRegistry
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
		containerClients:  initContainerClients(ctx),
		handlerRegistry:   NewHandlerRegistry(),
		allProcesses:      allProcs,
	}, nil
}

func (d *discoverer) Close() error {
	return nil
}

// logger returns the caller-supplied slog logger or a discard logger if
// none was provided. Always safe to call; never returns nil.
func (d *discoverer) logger() *slog.Logger {
	if d.opts.Logger == nil {
		return discardLogger
	}
	return d.opts.Logger
}

// --- Single-pass classification ---

// classifyAll runs every scanned process through the handler registry and
// returns them grouped by language. The first handler whose Detect returns
// true wins (same as mw-lang-detector's DetectLanguage pattern).
func (d *discoverer) classifyAll() map[Language][]ProcessInfo {
	start := time.Now()
	grouped := make(map[Language][]ProcessInfo)
	for i := range d.allProcesses {
		if lang, ok := d.handlerRegistry.Detect(&d.allProcesses[i]); ok {
			grouped[lang] = append(grouped[lang], d.allProcesses[i])
		}
	}

	// Build per-language counts for the log record. Zero-count languages
	// are omitted to keep output tidy.
	counts := make([]any, 0, 2+2*len(grouped))
	counts = append(counts,
		"scanned_pids", len(d.allProcesses),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	for lang, infos := range grouped {
		counts = append(counts, string(lang)+"_count", len(infos))
	}
	d.logger().Debug("classification complete", counts...)

	return grouped
}

// --- Generic enrichment worker pool ---

// enrichWithWorkerPool runs the handler's Enrich and PassesFilter methods
// across a concurrent worker pool for a set of classified processes.
func (d *discoverer) enrichWithWorkerPool(ctx context.Context, infos []ProcessInfo, handler LanguageHandler, opts DiscoveryOptions) ([]*Process, error) {
	if len(infos) == 0 {
		return []*Process{}, nil
	}

	lang := string(handler.Lang())
	enrichStart := time.Now()

	type result struct {
		proc *Process
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
					return
				default:
				}
				proc := handler.Enrich(info, opts, d.containerDetector)
				select {
				case results <- result{proc, nil}:
				case <-ctx.Done():
					return
				}
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

	var processes []*Process
	for r := range results {
		if r.err != nil {
			continue
		}
		if r.proc != nil {
			if handler.PassesFilter(r.proc, opts.Filter) {
				processes = append(processes, r.proc)
			}
		}
	}

	enrichDuration := time.Since(enrichStart)

	// Attach listening-port info as a post-enrichment batch. Grouping by
	// netns lets us read /proc/<pid>/net/* once per unique namespace instead
	// of once per PID — a big win on container hosts where many processes
	// share the host netns.
	portsStart := time.Now()
	AttachListeners(processes)
	portsDuration := time.Since(portsStart)

	// Batch-resolve container names via runtime API instead of per-process
	// CLI calls. Uses the global name cache to avoid redundant lookups.
	containerStart := time.Now()
	batchResolveContainerNames(ctx, processes, d.containerClients)

	// Container names were not yet available when extractServiceName ran
	// during enrichment. For container processes that now have a resolved
	// name, promote it to ServiceName if no higher-priority source
	// (like OTEL_SERVICE_NAME) already set it.
	applyContainerServiceNames(processes)
	containerDuration := time.Since(containerStart)

	d.logger().Debug("enrichment complete",
		"language", lang,
		"classified_count", len(infos),
		"filtered_count", len(processes),
		"enrich_ms", enrichDuration.Milliseconds(),
		"ports_ms", portsDuration.Milliseconds(),
		"container_ms", containerDuration.Milliseconds(),
		"workers", numWorkers,
	)

	return processes, nil
}

// --- Registry-driven discovery ---

// DiscoverAll classifies and enriches all scanned processes, returning
// results grouped by language. Each language's handler is used for
// enrichment and filtering.
func (d *discoverer) DiscoverAll(ctx context.Context) (map[Language][]*Process, error) {
	var cancel context.CancelFunc
	if d.opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, d.opts.Timeout)
		defer cancel()
	}

	overallStart := time.Now()
	grouped := d.classifyAll()
	result := make(map[Language][]*Process)

	for _, handler := range d.handlerRegistry.Handlers() {
		lang := handler.Lang()
		infos := grouped[lang]
		if len(infos) == 0 {
			continue
		}

		procs, err := d.enrichWithWorkerPool(ctx, infos, handler, d.opts)
		if err != nil {
			continue
		}
		result[lang] = procs
	}

	total := 0
	for _, procs := range result {
		total += len(procs)
	}
	d.logger().Info("discovery complete",
		"duration_ms", time.Since(overallStart).Milliseconds(),
		"total_processes", total,
		"languages", len(result),
	)

	return result, nil
}

// DiscoverByLanguage returns enriched processes for a specific language.
func (d *discoverer) DiscoverByLanguage(ctx context.Context, lang Language) ([]*Process, error) {
	var cancel context.CancelFunc
	if d.opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, d.opts.Timeout)
		defer cancel()
	}

	handler := d.handlerRegistry.ForLanguage(lang)
	if handler == nil {
		return nil, fmt.Errorf("no handler registered for language: %s", lang)
	}

	grouped := d.classifyAll()
	infos := grouped[lang]

	return d.enrichWithWorkerPool(ctx, infos, handler, d.opts)
}

// --- Public convenience functions ---

// FindAllProcesses discovers all supported language processes, grouped by language.
// Use FindAllProcessesWithLogger to receive structured timing logs.
func FindAllProcesses(ctx context.Context) (map[Language][]*Process, error) {
	return FindAllProcessesWithLogger(ctx, nil)
}

// FindAllProcessesWithLogger is like FindAllProcesses but emits structured
// timing/diagnostic logs via the supplied slog logger. A nil logger disables
// logging (equivalent to FindAllProcesses).
func FindAllProcessesWithLogger(ctx context.Context, logger *slog.Logger) (map[Language][]*Process, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true
	opts.Logger = logger

	d, err := NewDiscovererWithOptions(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("error discovering processes: %w", err)
	}
	defer d.Close()

	return d.DiscoverAll(ctx)
}

// FindProcessesByLanguage discovers processes for a specific language.
// Use FindProcessesByLanguageWithLogger to receive structured timing logs.
func FindProcessesByLanguage(ctx context.Context, lang Language) ([]*Process, error) {
	return FindProcessesByLanguageWithLogger(ctx, lang, nil)
}

// FindProcessesByLanguageWithLogger is like FindProcessesByLanguage but
// emits structured timing/diagnostic logs via the supplied slog logger.
// A nil logger disables logging.
func FindProcessesByLanguageWithLogger(ctx context.Context, lang Language, logger *slog.Logger) ([]*Process, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true
	opts.Logger = logger

	d, err := NewDiscovererWithOptions(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("error discovering %s processes: %w", lang, err)
	}
	defer d.Close()

	return d.DiscoverByLanguage(ctx, lang)
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

func currentUser() string {
	return os.Getenv("USER")
}
