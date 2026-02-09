package otelinject

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/k0kubun/pp"
	"github.com/middleware-labs/java-injector/pkg/discovery"
)

const NODE_OTEL_AGENT_PATH = ""

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
		return nil, fmt.Errorf(
		return nil, fmt.Errorf("error creating NodeSystemdInjector: %w", err)
			err.Error(),
		)
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
		} else {
			// pp.Println("Got a systemd process --> ", proc)
			err := n.InjectOtelInstrumentation(&proc)
			if err != nil {
				errorsInstrumentation = errors.Join(errorsInstrumentation, err)
			}
		}
	}
	return errorsInstrumentation
}

func (n *NodeSystemdInjector) InjectOtelInstrumentation(proc *discovery.NodeProcess) error {
	// 3. Create Drop-in, Reload and Restart
	dropInConfig, err := NewSystemdDropin(proc.ProcessPID)
	if err != nil {
		return fmt.Errorf("could not create drop-in config for process %d (%s): %w", proc.ProcessPID, proc.ServiceName, err)

	}
	if err := dropInConfig.applySystemdDropIn(); err != nil {
		return err
	}

	return nil
}

// Helper function to extract service name from cgroup lines
func extractServiceNameFromCgroup(lines []string) string {
	for _, line := range lines {
		// Example cgroup line: 0::/system.slice/myservice.service
		if strings.Contains(line, ".service") {
			parts := strings.Split(line, "/")
			for _, part := range parts {
				if strings.HasSuffix(part, ".service") {
					return part
				}
			}
		}
	}
	return ""
}
