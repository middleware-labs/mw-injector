package otelinject

import (
	"context"
	"errors"
	"fmt"

	"github.com/k0kubun/pp"
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
	NodeProcs []discovery.NodeProcess
	Status    NodeAgentStatus
}

func NewNodeSystemdInjector() (*NodeSystemdInjector, error) {
	ctx := context.Background()

	pp.Println("CREATING NEW NodeSystemdInjector")
	nodeProcs, err := discovery.FindAllNodeProcesses(ctx)
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
		if !proc.IsSystemdProcess() {
			continue
		}
		// pp.Println("Got a systemd process --> ", proc)
		dropIn, err := NewSystemdDropin(proc.ProcessPID)
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
