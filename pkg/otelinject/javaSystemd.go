package otelinject

import (
	"context"
	"errors"
	"fmt"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

type JavaAgentStatus struct {
	Ready                     bool     `json:"ready"`
	InjectorSharedObjectFound bool     `json:"libotelinject.so_found"`
	JavaAgentJarFound         bool     `json:"javaagent.jar_found"`
	Errors                    []string `json:"errors,omitempty"`
}

type JavaSystemdInjector struct {
	JavaProcs []discovery.JavaProcess
	Status    JavaAgentStatus
}

func NewJavaSystemdInjector() (*JavaSystemdInjector, error) {
	ctx := context.Background()
	javaProcs, err := discovery.FindAllJavaProcesses(ctx)
	if err != nil {
		return nil, fmt.Errorf("error creating JavaSystemdInjector: %w", err)
	}

	ret := &JavaSystemdInjector{
		JavaProcs: javaProcs,
	}

	ret.ValidateAssets("")

	return ret, nil
}

func (j *JavaSystemdInjector) ValidateAssets(baseDir string) bool {
	j.Status = ValidateJavaAgent(baseDir)
	return j.Status.Ready
}

func (j *JavaSystemdInjector) Instrument() error {
	var errs error
	if !j.Status.Ready {
		errs = errors.Join(errs, fmt.Errorf("java agent not found"))
		return errs
	}
	for _, proc := range j.JavaProcs {
		isSystemd, unitName := checkSystemdStatus(proc.ProcessPID)

		if !isSystemd {
			continue
		}
		dropIn, err := NewSystemdDropin(unitName)
		if err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf(
					"could not create a new dropIn for %s and pid %d, %w",
					unitName,
					proc.ProcessPID,
					err,
				),
			)
			continue
		}

		if err := dropIn.applySystemdDropIn(); err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf(
					"could not apply dropIn for %s and pid %d, %w",
					unitName,
					proc.ProcessPID,
					err,
				),
			)
		}

	}

	return errs
}
