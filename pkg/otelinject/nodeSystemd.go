package otelinject

import (
	"context"
	"fmt"

	"github.com/k0kubun/pp"
	"github.com/middleware-labs/java-injector/pkg/discovery"
)

const NODE_OTEL_AGENT_PATH = ""

// NodeAgentStatus holds the result of the validation.
type NodeAgentStatus struct {
	Ready           bool     `json:"ready"`
	RegisterJSFound bool     `json:"register_js_found"`
	PackageVersion  string   `json:"package_version,omitempty"`
	MissingDeps     []string `json:"missing_deps,omitempty"`
	Errors          []string `json:"errors,omitempty"`
}

type NodeSystemdDropin struct {
	ld_preload        string `json:"LD_PRELOAD"`
	service_name      string `json:"OTEL_SERVICE_NAME"`
	exporter_endpoint string `json:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	otlp_headers      string `json:"OTEL_EXPORTER_OTLP_HEADERS"`
}

type NodeSystemdInjector struct {
	NodeProcs []discovery.NodeProcess
	Status    NodeAgentStatus
	DropIn    NodeSystemdDropin
}

func NewNodeSystemdInjector() (*NodeSystemdInjector, error) {
	ctx := context.Background()
	pp.Println("CREATING NEW NodeSystemdInjector")
	nodeProcs, err := discovery.FindAllNodeProcesses(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"error in creating NodeSystemdInjector, %s",
			err.Error(),
		)
	}

	return &NodeSystemdInjector{
		NodeProcs: nodeProcs,
		Status:    ValidateNodeAgent(""),
	}, nil
}

func (n NodeSystemdInjector) ValidateAssets(baseDir string) bool {
	return n.Status.Ready
}

func (n NodeSystemdInjector) Instrument() error {
	if n.Status.Ready == false {
		return fmt.Errorf("NodeSystemdInjector is not ready")
	}

}
