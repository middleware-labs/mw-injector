package types

// CommandHandler defines the interface for all CLI commands
type CommandHandler interface {
	Execute() error
	GetDescription() string
}

// CommandConfig holds configuration passed to all commands
type CommandConfig struct {
	DefaultAgentDir      string
	DefaultAgentName     string
	DefaultAgentPath     string
	DefaultNodeAgentPath string
}

// NewDefaultConfig returns default configuration values
func NewDefaultConfig() *CommandConfig {
	return &CommandConfig{
		DefaultAgentDir:      "/opt/middleware/agents",
		DefaultAgentName:     "middleware-javaagent-1.8.1.jar",
		DefaultAgentPath:     "/opt/middleware/agents/middleware-javaagent-1.8.1.jar",
		DefaultNodeAgentPath: "/opt/middleware/agents/autoinstrumentation",
	}
}

// NoArgsCommand represents commands that take no arguments
type NoArgsCommand interface {
	CommandHandler
}

// SingleArgCommand represents commands that take one argument
type SingleArgCommand interface {
	CommandHandler
	SetArg(arg string)
}
