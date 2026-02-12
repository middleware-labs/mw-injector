package otelinject

import (
	"context"
	"errors"
	"fmt"

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
		isSystemd, cleanName := checkSystemdStatus(proc.ProcessPID)
		if !isSystemd {
			continue
		}
		dropIn, err := NewSystemdDropin(proc.ProcessPID, cleanName)
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
