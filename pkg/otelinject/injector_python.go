// injector_python.go implements OtelInjector for Python processes. It discovers
// running Python processes via the discovery package, validates the Python OTel
// agent installation, and instruments/uninstruments them via systemd drop-in
// files with Python-specific environment variables (PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX).
package otelinject

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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
	PythonProcs []*discovery.Process
	Status      PythonAgentStatus
}

func NewPythonSystemdInjector() (*PythonSystemdInjector, error) {
	return NewPythonSystemdInjectorWithLogger(nil)
}

// NewPythonSystemdInjectorWithLogger is like NewPythonSystemdInjector but
// threads an optional slog logger through the discovery call so timing
// records are emitted. A nil logger disables logging.
func NewPythonSystemdInjectorWithLogger(logger *slog.Logger) (*PythonSystemdInjector, error) {
	ctx := context.Background()
	pythonProcs, err := discovery.FindProcessesByLanguageWithLogger(ctx, discovery.LangPython, logger)
	if err != nil {
		return nil, fmt.Errorf("error creating PythonSystemdInjector: %w", err)
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

	// Dedup by unit name to prevent cascading restarts for multi-process
	// apps (e.g. Gunicorn with 4 workers sharing one systemd unit).
	processedUnits := make(map[string]bool)
	for _, proc := range p.PythonProcs {
		isSystemd, unitName := discovery.CheckSystemdStatus(proc.PID)

		if !isSystemd {
			continue
		}

		if processedUnits[unitName] {
			continue
		}

		processedUnits[unitName] = true

		dropIn, err := NewSystemdDropin(unitName)
		if err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf(
					"failed to create systemd dropin for %d-%s: %w",
					proc.PID,
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
					proc.PID,
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
		isSystemd, unitName := discovery.CheckSystemdStatus(proc.PID)
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
					proc.PID,
					unitName,
					err,
				),
			)
		}
	}
	return errs
}

func (p *PythonSystemdInjector) InstrumentService(service discovery.ServiceSetting) error {
	proc := p.getProcToInstrument(service.PID)
	if proc == nil {
		return fmt.Errorf("could not find python process: %v running on the host", service)
	}
	isSystemd, unitName := discovery.CheckSystemdStatus(proc.PID)
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

func (p *PythonSystemdInjector) getProcToInstrument(pid int32) *discovery.Process {
	for _, proc := range p.PythonProcs {
		if proc.PID == pid {
			return proc
		}
	}
	return nil
}
