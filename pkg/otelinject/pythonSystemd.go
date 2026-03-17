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

	// 1. DEDUPLICATION MAP
	// Track which units we have already instrumented in this run to prevent
	// cascading restarts for multi-process apps (e.g. Gunicorn w/ 4 workers).
	processedUnits := make(map[string]bool)
	for _, proc := range p.PythonProcs {
		isSystemd, unitName := discovery.CheckSystemdStatus(proc.ProcessPID)

		if !isSystemd {
			continue
		}

		if processedUnits[unitName] {
			continue // We already handled this service, skip this worker PID
		}

		processedUnits[unitName] = true

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
	processedUnits := make(map[string]bool)
	for _, proc := range p.PythonProcs {
		isSystemd, unitName := discovery.CheckSystemdStatus(proc.ProcessPID)
		if !isSystemd {
			continue
		}
		if processedUnits[unitName] {
			continue
		}
		processedUnits[unitName] = true
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

func (p *PythonSystemdInjector) InstrumentService(service discovery.ServiceSetting) error {
	pythonProcToInstrument := p.getPythonProcToInstrument(service.PID)
	if pythonProcToInstrument == nil {
		return fmt.Errorf("could not find python process: %v running on the host", service)
	}
	isSystemd, unitName := discovery.CheckSystemdStatus(pythonProcToInstrument.ProcessPID)
	if !isSystemd {
		return fmt.Errorf("given python process is not a systemd process: %v", service)
	}
	dropIn, err := NewSystemdDropin(unitName)
	if err != nil {
		return fmt.Errorf(
			"could not create a new dropIn for python process %s and pid %d, %w",
			unitName,
			service.PID,
			err,
		)
	}
	if err := dropIn.applySystemdDropInPython(); err != nil {
		return fmt.Errorf("could not apply dropIn for %s and pid %d, %w", unitName, service.PID, err)
	}

	return nil
}

func (p *PythonSystemdInjector) getPythonProcToInstrument(pid int32) *discovery.PythonProcess {
	for _, proc := range p.PythonProcs {
		if proc.ProcessPID == pid {
			return &proc
		}
	}
	return nil
}
