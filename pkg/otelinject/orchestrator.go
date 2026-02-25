package otelinject

import (
	"errors"
	"fmt"
	"net/url"
	"runtime"

	"github.com/k0kubun/pp"
	"github.com/middleware-labs/java-injector/pkg/discovery"
)

const apiPathForAgentSetting = "api/v1/agent/public/setting/"

func ReportStatus(
	hostname string,
	apiKey string,
	urlForConfigCheck string,
	version string,
	infraPlatform string,
) error {
	u, err := url.Parse(urlForConfigCheck)
	if err != nil {
		return err
	}
	baseURL := u.JoinPath(apiPathForAgentSetting, apiKey, hostname)

	client, err := discovery.NewAgentAPIClient(
		discovery.AgentAPIClientConfig{
			BaseURL:       baseURL.String(),
			APIKey:        apiKey,
			Version:       "v1",
			InfraPlatform: infraPlatform,
			Hostname:      hostname,
		},
	)

	if err != nil {
		return fmt.Errorf("failed to create api client for injector, ", err)
	}

	// Download current HostSettings from PG DB
	agentHostSettings, err := client.GetAgentHostSettings()
	if err != nil {
		pp.Println("ERROR: %w", err)
	}

	// Just take out Auto Instrumentation Settings
	autoInstrumentationSetting, err := discovery.GetAutoInstrumentationSettings(agentHostSettings)
	if err != nil {
		pp.Println("ERROR: %w", err)
	}

	// Filter Auto Instrumentation Settings
	servicesToBeInstrumented := discovery.FilterServices(autoInstrumentationSetting, func(s discovery.ServiceSetting) bool {
		return s.InstrumentThis == true
	})

	pp.Println("Services to be instrumented:", servicesToBeInstrumented)
	pp.Println("AutoInstrumentationSetting:", autoInstrumentationSetting)

	rawReportValue, err := discovery.GetAgentReportValue()
	if err != nil {
		return fmt.Errorf("failed to generate agent report value: %w", err)
	}

	pp.Println("From DB:", autoInstrumentationSetting)
	pp.Println("Raw report value:", rawReportValue[runtime.GOOS].AutoInstrumentationSettings)

	servicesToBeInstrumented = discovery.FilterInstrumentable(autoInstrumentationSetting, rawReportValue[runtime.GOOS].AutoInstrumentationSettings)

	pp.Println("Finally, new we'd be instrumenting these: ", servicesToBeInstrumented)

	err = InjectServices(servicesToBeInstrumented)

	if err != nil {
		return fmt.Errorf("failed to instrument services: %w. %w", servicesToBeInstrumented, err)
	}
	//---------------------------------------------------------------------
	// Sending the report
	//

	postInstrumentationReportValue, err := discovery.GetAgentReportValue()

	if err := client.ReportStatus(postInstrumentationReportValue); err != nil {
		return fmt.Errorf("failed to send report: %w", err)
	}
	return nil
}

func InjectServices(services map[string]discovery.ServiceSetting) error {
	var errs error

	JavaSystemdInjector, err := NewJavaSystemdInjector()
	if err != nil {
		errs = errors.Join(
			errs,
			fmt.Errorf("could not create a new Java SystemD Injector %w", err),
		)
		return errs
	}

	PythonSystemdInjector, err := NewPythonSystemdInjector()
	if err != nil {
		errs = errors.Join(
			errs,
			fmt.Errorf("could not create a new Python SystemD Injector %w", err),
		)
		return errs
	}

	NodeSystemdInjector, err := NewNodeSystemdInjector()
	if err != nil {
		errs = errors.Join(
			errs,
			fmt.Errorf("could not create a new Node SystemD Injector %w", err),
		)
	}

	for _, service := range services {
		pp.Println("Trying to instrument service:", service.ServiceName, service.PID)
		switch service.Language {
		case "java":
			err := JavaSystemdInjector.InstrumentService(service)
			if err != nil {
				errs = errors.Join(
					errs,
					fmt.Errorf("could not instrument java service %s and pid %d, %w",
						service.ServiceName, service.PID, err,
					),
				)
			}
		case "python":
			err := PythonSystemdInjector.InstrumentService(service)
			if err != nil {
				errs = errors.Join(
					errs,
					fmt.Errorf("could not instrument python service %s and pid %d, %w",
						service.ServiceName, service.PID, err,
					),
				)
			}

		case "node":
			err := NodeSystemdInjector.InstrumentService(service)
			if err != nil {
				errs = errors.Join(
					errs,
					fmt.Errorf("could not instrument node service %s and pid %d, %w",
						service.ServiceName, service.PID, err,
					),
				)
			}
		default:
			errs = errors.Join(
				errs,
				fmt.Errorf("unsupported language %s", service.Language),
			)
		}

	}
	return errs
}
