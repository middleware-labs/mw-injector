package otelinject

import (
	"context"
	"errors"
	"fmt"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

type PythonAgentStatus struct {
	Ready                     bool     `json:"ready"`
	InjectorSharedObjectFound bool     `json:"libotelinject.so_found"`
	LibcFlavor                string   `json:"system_libc_flavor"`
	TargetDirFound            bool     `json:"target_flavor_dir_found"`
	SiteCustomizeFound        bool     `json:"sitecustomize_found"`
	Errors                    []string `json:"errors,omitempty"`
}

type PythonSystemdInjector struct {
	PythonProcs []discovery.PythonProcess
	Status      PythonAgentStatus
}

func NewPythonSystemdInjector() (*PythonSystemdInjector, error) {
	ctx := context.Background()
	pythonProcs, err := discovery.FindAllPythonProcess(ctx)
	if err != nil {
		return nil, fmt.Errorf("Error in creating PythonSystemdInjector: %w", err)
	}

	ret := &PythonSystemdInjector{
		PythonProcs: pythonProcs,
	}

	ret.ValidateAssets("")

	return ret, nil
}

func (p *PythonSystemdInjector) ValidateAssets(baseDir string) bool {
	p.Status = ValidatePythonAgent(baseDir)
	return p.Status.Ready
}

func (p *PythonSystemdInjector) Instrument() error {
	var errs error

	if !p.Status.Ready {
		errs = errors.Join(errs, fmt.Errorf("python agent not found"))
		return errs
	}

	for _, proc := range p.PythonProcs {
		isSystemd, unitName := checkSystemdStatus(proc.ProcessPID)

		if !isSystemd {
			continue
		}

		dropIn, err := NewSystemdDropin(unitName)
		if err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf(
					"failed to create systemd dropin for %d-%s: %w",
					proc.ProcessPID,
					unitName,
					err,
				),
			)
			continue
		}

		if err := dropIn.applySystemdDropInPython(); err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf(
					"failed to apply systemd dropin for %d-%s: %w",
					proc.ProcessPID,
					unitName,
					err,
				),
			)
		}

	}

	return errs
}

func (p *PythonSystemdInjector) Uninstrument() error {
	var errs error
	for _, proc := range p.PythonProcs {
		isSystemd, unitName := checkSystemdStatus(proc.ProcessPID)
		if !isSystemd {
			continue
		}

		if err := removeSystemdDropIn(unitName); err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf(
					"failed to remove systemd dropin for %d-%s: %w",
					proc.ProcessPID,
					unitName,
					err,
				),
			)
		}
	}
	return errs
}
