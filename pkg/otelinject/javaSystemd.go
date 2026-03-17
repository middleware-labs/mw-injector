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
		isSystemd, unitName := discovery.CheckSystemdStatus(proc.ProcessPID)

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

func (j *JavaSystemdInjector) Uninstrument() error {
	var errs error
	for _, proc := range j.JavaProcs {
		isSystemd, unitName := discovery.CheckSystemdStatus(proc.ProcessPID)
		if !isSystemd {
			continue
		}

		if err := removeSystemdDropIn(unitName); err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf("could not remove dropIn for %s and pid %d, %w", unitName, proc.ProcessPID, err),
			)
		}
	}
	return errs
}

func (j *JavaSystemdInjector) InstrumentService(service discovery.ServiceSetting) error {
	javaProcToInstrument := j.getJavaProcToInstrument(service.PID)
	if javaProcToInstrument == nil {
		return fmt.Errorf("could not find java process: %v running on the host", service)
	}
	isSystemd, unitName := discovery.CheckSystemdStatus(javaProcToInstrument.ProcessPID)
	if !isSystemd {
		return fmt.Errorf("given java process is not a systemd process: %v", service)
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
	if err := dropIn.applySystemdDropIn(); err != nil {
		return fmt.Errorf("could not apply dropIn for %s and pid %d, %w", unitName, service.PID, err)
	}

	return nil
}

func (j *JavaSystemdInjector) getJavaProcToInstrument(pid int32) *discovery.JavaProcess {
	for _, proc := range j.JavaProcs {
		if proc.ProcessPID == pid {
			return &proc
		}
	}
	return nil
}
