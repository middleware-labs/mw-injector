// injector_java.go implements OtelInjector for Java processes. It discovers
// running Java processes via the discovery package, validates that the Java
// agent JAR and libotelinject.so are present, and instruments/uninstruments
// them via systemd drop-in files.
package otelinject

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/middleware-labs/java-injector/pkg/discovery"
)

type JavaAgentStatus struct {
	Ready                     bool     `json:"ready"`
	InjectorSharedObjectFound bool     `json:"libotelinject.so_found"`
	JavaAgentJarFound         bool     `json:"javaagent.jar_found"`
	Errors                    []string `json:"errors,omitempty"`
}

type JavaSystemdInjector struct {
	JavaProcs []*discovery.Process
	Status    JavaAgentStatus
}

func NewJavaSystemdInjector() (*JavaSystemdInjector, error) {
	return NewJavaSystemdInjectorWithLogger(nil)
}

// NewJavaSystemdInjectorWithLogger is like NewJavaSystemdInjector but
// threads an optional slog logger through the discovery call so timing
// records are emitted. A nil logger disables logging.
func NewJavaSystemdInjectorWithLogger(logger *slog.Logger) (*JavaSystemdInjector, error) {
	ctx := context.Background()
	javaProcs, err := discovery.FindProcessesByLanguageWithLogger(ctx, discovery.LangJava, logger)
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
		isSystemd, unitName := discovery.CheckSystemdStatus(proc.PID)

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
					proc.PID,
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
					proc.PID,
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
		isSystemd, unitName := discovery.CheckSystemdStatus(proc.PID)
		if !isSystemd {
			continue
		}

		if err := removeSystemdDropIn(unitName); err != nil {
			errs = errors.Join(
				errs,
				fmt.Errorf("could not remove dropIn for %s and pid %d, %w", unitName, proc.PID, err),
			)
		}
	}
	return errs
}

func (j *JavaSystemdInjector) InstrumentService(service discovery.ServiceSetting) error {
	proc := j.getProcToInstrument(service.PID)
	if proc == nil {
		return fmt.Errorf("could not find java process: %v running on the host", service)
	}
	isSystemd, unitName := discovery.CheckSystemdStatus(proc.PID)
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

func (j *JavaSystemdInjector) getProcToInstrument(pid int32) *discovery.Process {
	for _, proc := range j.JavaProcs {
		if proc.PID == pid {
			return proc
		}
	}
	return nil
}
