package cli

import (
	"fmt"

	"github.com/middleware-labs/java-injector/pkg/cli/commands"
)

// Router handles command routing and execution
type Router struct {
	config   *CommandConfig
	commands map[string]CommandHandler
}

// NewRouter creates a new command router with default configuration
func NewRouter() *Router {
	config := NewDefaultConfig()
	router := &Router{
		config:   config,
		commands: make(map[string]CommandHandler),
	}

	router.registerCommands()
	return router
}

// NewRouterWithConfig creates a new command router with custom configuration
func NewRouterWithConfig(config *CommandConfig) *Router {
	router := &Router{
		config:   config,
		commands: make(map[string]CommandHandler),
	}

	router.registerCommands()
	return router
}

// registerCommands registers all available commands
func (r *Router) registerCommands() {
	// List commands
	// r.commands["list"] = commands.NewListCommand(r.config)
	// r.commands["list-docker"] = commands.NewListDockerCommand(r.config)
	// r.commands["list-all"] = commands.NewListAllCommand(r.config)
	r.commands["list-all"] = commands.NewListAllCommand(r.config)
	r.commands["list-systemd"] = commands.NewListSystemdCommand(r.config)
	r.commands["list-docker"] = commands.NewListDockerCommand(r.config)

	// Instrument commands
	r.commands["auto-instrument"] = commands.NewAutoInstrumentCommand(r.config)
	r.commands["instrument-docker"] = commands.NewInstrumentDockerCommand(r.config)
	r.commands["instrument-container"] = commands.NewInstrumentContainerCommand(r.config)

	// Uninstrument commands
	r.commands["uninstrument"] = commands.NewUninstrumentCommand(r.config)
	r.commands["uninstrument-docker"] = commands.NewUninstrumentDockerCommand(r.config)
	r.commands["uninstrument-container"] = commands.NewUninstrumentContainerCommand(r.config)
}

// Run executes the appropriate command based on command line arguments
func (r *Router) Run(args []string) error {
	if len(args) < 2 {
		PrintUsage()
		return fmt.Errorf("no command specified")
	}

	commandName := args[1]
	commandArgs := args[2:]

	// Handle help requests
	if commandName == "help" || commandName == "--help" || commandName == "-h" {
		PrintUsage()
		return nil
	}

	// Route to appropriate command
	switch commandName {
	// case "list", "list-docker", "list-all":
	// 	return r.executeNoArgsCommand(commandName, commandArgs)
	case "list-systemd", "list-docker", "list-all": // ✅ FIXED - changed "list" to "list-systemd"
		return r.executeNoArgsCommand(commandName, commandArgs)

	case "instrument-container", "uninstrument-container":
		return r.executeSingleArgCommand(commandName, commandArgs)

	case "auto-instrument", "instrument-docker", "uninstrument", "uninstrument-docker":
		return r.executeNoArgsCommand(commandName, commandArgs)

	// Add these cases to the switch statement in pkg/cli/router.go
	case "auto-instrument-config":
		return r.executeOptionalArgCommand(commandName, commandArgs)

	case "instrument-docker-config":
		return r.executeOptionalArgCommand(commandName, commandArgs)

	default:
		PrintUsage()
		return fmt.Errorf("unknown command: %s", commandName)
	}
}

// executeNoArgsCommand executes commands that take no arguments
func (r *Router) executeNoArgsCommand(commandName string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("command '%s' does not accept arguments", commandName)
	}

	cmd, exists := r.commands[commandName]
	if !exists {
		return fmt.Errorf("command '%s' not implemented yet", commandName)
	}

	return cmd.Execute()
}

// executeSingleArgCommand executes commands that take exactly one argument
func (r *Router) executeSingleArgCommand(commandName string, args []string) error {
	if len(args) < 1 {
		return r.getSingleArgUsageError(commandName)
	}

	if len(args) > 1 {
		return fmt.Errorf("command '%s' accepts only one argument", commandName)
	}

	cmd, exists := r.commands[commandName]
	if !exists {
		return fmt.Errorf("command '%s' not implemented yet", commandName)
	}

	// Set the argument on the command if it supports it
	if singleArgCmd, ok := cmd.(SingleArgCommand); ok {
		singleArgCmd.SetArg(args[0])
		return singleArgCmd.Execute()
	}

	return fmt.Errorf("command '%s' does not support single argument execution", commandName)
}

func (r *Router) executeOptionalArgCommand(commandName string, args []string) error {
	var configPath string
	if len(args) > 0 {
		configPath = args[0]
	}

	switch commandName {
	case "auto-instrument-config":
		cmd := commands.NewConfigAutoInstrumentCommand(r.config, configPath)
		return cmd.Execute()
	case "instrument-docker-config":
		cmd := commands.NewConfigInstrumentDockerCommand(r.config, configPath)
		return cmd.Execute()
	default:
		return fmt.Errorf("unknown optional arg command: %s", commandName)
	}
}

// getSingleArgUsageError returns appropriate usage error for single-arg commands
func (r *Router) getSingleArgUsageError(commandName string) error {
	switch commandName {
	case "instrument-container":
		return fmt.Errorf("❌ Container name required\nUsage: mw-injector instrument-container <container-name>")
	case "uninstrument-container":
		return fmt.Errorf("❌ Container name required\nUsage: mw-injector uninstrument-container <container-name>")
	default:
		return fmt.Errorf("❌ Argument required for command: %s", commandName)
	}
}

// GetAvailableCommands returns a list of all registered commands
func (r *Router) GetAvailableCommands() []string {
	var commands []string
	for cmd := range r.commands {
		commands = append(commands, cmd)
	}
	return commands
}

// GetCommandDescription returns the description for a specific command
func (r *Router) GetCommandDescription(commandName string) string {
	if cmd, exists := r.commands[commandName]; exists {
		return cmd.GetDescription()
	}
	return "Unknown command"
}
