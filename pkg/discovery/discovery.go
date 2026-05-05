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
}

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
		handlerRegistry:   NewHandlerRegistry(),
		allProcesses:      allProcs,
	}, nil
}

func (d *discoverer) Close() error {
	return nil
}

// --- Single-pass classification ---

// classifyAll runs every scanned process through the handler registry and
// returns them grouped by language. The first handler whose Detect returns
// true wins (same as mw-lang-detector's DetectLanguage pattern).
func (d *discoverer) classifyAll() map[Language][]ProcessInfo {
	grouped := make(map[Language][]ProcessInfo)
	for i := range d.allProcesses {
		if lang, ok := d.handlerRegistry.Detect(&d.allProcesses[i]); ok {
			grouped[lang] = append(grouped[lang], d.allProcesses[i])
		}
	}
	return grouped
}

// --- Generic enrichment worker pool ---

// enrichWithWorkerPool runs the handler's Enrich and PassesFilter methods
// across a concurrent worker pool for a set of classified processes.
func (d *discoverer) enrichWithWorkerPool(ctx context.Context, infos []ProcessInfo, handler LanguageHandler, opts DiscoveryOptions) ([]*Process, error) {
	if len(infos) == 0 {
		return []*Process{}, nil
	}

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
func FindAllProcesses(ctx context.Context) (map[Language][]*Process, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true

	d, err := NewDiscovererWithOptions(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("error discovering processes: %w", err)
	}
	defer d.Close()

	return d.DiscoverAll(ctx)
}

// FindProcessesByLanguage discovers processes for a specific language.
func FindProcessesByLanguage(ctx context.Context, lang Language) ([]*Process, error) {
	opts := DefaultDiscoveryOptions()
	opts.ExcludeContainers = false
	opts.IncludeContainerInfo = true

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
