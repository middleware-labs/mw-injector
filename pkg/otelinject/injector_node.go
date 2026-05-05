// injector_node.go implements OtelInjector for Node.js processes. It discovers
// running Node processes via the discovery package, validates that the Node
// agent register.js and libotelinject.so are present, and instruments/uninstruments
// them via systemd drop-in files.
package otelinject

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

// NodeAgentStatus holds the result of the validation.
type NodeAgentStatus struct {
	Ready                     bool     `json:"ready"`
	InjectorSharedObjectFound bool     `json:"libotelinject.so_found"`
	RegisterJSFound           bool     `json:"register_js_found"`
	PackageVersion            string   `json:"package_version,omitempty"`
	MissingDeps               []string `json:"missing_deps,omitempty"`
	Errors                    []string `json:"errors,omitempty"`
}

type NodeSystemdInjector struct {
	NodeProcs []*discovery.Process
	Status    NodeAgentStatus
}

func NewNodeSystemdInjector() (*NodeSystemdInjector, error) {
	return NewNodeSystemdInjectorWithLogger(nil)
}

// NewNodeSystemdInjectorWithLogger is like NewNodeSystemdInjector but
// threads an optional slog logger through the discovery call so timing
// records are emitted. A nil logger disables logging.
func NewNodeSystemdInjectorWithLogger(logger *slog.Logger) (*NodeSystemdInjector, error) {
	ctx := context.Background()
	nodeProcs, err := discovery.FindProcessesByLanguageWithLogger(ctx, discovery.LangNode, logger)
	if err != nil {
		return nil, fmt.Errorf("error creating NodeSystemdInjector: %w", err)
	}

	ret := &NodeSystemdInjector{
		NodeProcs: nodeProcs,
	}

	ret.ValidateAssets("")

	return ret, nil
}

func (n *NodeSystemdInjector) ValidateAssets(baseDir string) bool {
	n.Status = ValidateNodeAgent(baseDir)
	return n.Status.Ready
}

func (n *NodeSystemdInjector) Instrument() error {
	if !n.Status.Ready {
		return fmt.Errorf("NodeSystemdInjector is not ready")
	}
	var errorsInstrumentation error
	for _, proc := range n.NodeProcs {
		isSystemd, cleanName := discovery.CheckSystemdStatus(proc.PID)
		if !isSystemd {
			continue
		}
		dropIn, err := NewSystemdDropin(cleanName)
		if err != nil {
			errorsInstrumentation = errors.Join(errorsInstrumentation, err)
			continue
		}

		if err := dropIn.applySystemdDropIn(); err != nil {
			errorsInstrumentation = errors.Join(errorsInstrumentation, err)
		}
	}
	return errorsInstrumentation
}

func (n *NodeSystemdInjector) Uninstrument() error {
	var errs error
	for _, proc := range n.NodeProcs {
		isSystemd, cleanName := discovery.CheckSystemdStatus(proc.PID)
		if !isSystemd {
			continue
		}

		if err := removeSystemdDropIn(cleanName); err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf("could not remove dropIn for %s and pid %d, %w", cleanName, proc.PID, err),
			)
		}
	}
	return errs
}

func (n *NodeSystemdInjector) InstrumentService(service discovery.ServiceSetting) error {
	proc := n.getProcToInstrument(service.PID)
	if proc == nil {
		return fmt.Errorf("could not find node process: %v running on the host", service)
	}
	isSystemd, unitName := discovery.CheckSystemdStatus(proc.PID)
	if !isSystemd {
		return fmt.Errorf("given node process is not a systemd process: %v", service)
	}
	dropIn, err := NewSystemdDropin(unitName)
	if err != nil {
		return fmt.Errorf(
			"could not create a new dropIn for %s and pid %d, %w",
			unitName,
			service.PID,
			err,
		)
	}
	// Node.js instrumentation uses the same LD_PRELOAD drop-in as Java.
	// libotelinject.so handles both runtimes, so NODE_OPTIONS is not required.
	if err := dropIn.applySystemdDropIn(); err != nil {
		return fmt.Errorf("could not apply dropIn for %s and pid %d, %w", unitName, service.PID, err)
	}

	return nil
}

func (n *NodeSystemdInjector) getProcToInstrument(pid int32) *discovery.Process {
	for _, proc := range n.NodeProcs {
		if proc.PID == pid {
			return proc
		}
	}
	return nil
}
