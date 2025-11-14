package docker

const (
	// Label keys for instrumentation metadata
	LabelInstrumented   = "middleware.instrumented"
	LabelInstrumentedAt = "middleware.instrumented_at"
	LabelAgentPath      = "middleware.agent_path"
	LabelOriginalEnv    = "middleware.original_env"
	LabelComposeFile    = "middleware.compose_file"
	LabelComposeService = "middleware.compose_service"
	LabelOriginalConfig = "middleware.original_config"
	LabelServiceName    = "middleware.service_name"
)
