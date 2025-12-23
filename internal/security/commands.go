package security

import (
	"fmt"
	"strings"
)

// CommandPolicy defines how custom functions should be restricted
type CommandPolicy struct {
	// AllowRemoteCommands determines if commands from remote configs should be executed
	// When false, nodes will reject configs that contain custom functions
	AllowRemoteCommands bool
	// AllowedCommands is a list of allowed command prefixes (e.g., "echo", "/usr/bin/curl")
	// Only used when AllowRemoteCommands is true
	AllowedCommands []string
}

// ValidateCommand checks if a command is allowed by the policy
func (p *CommandPolicy) ValidateCommand(cmdParts []string) error {
	if len(cmdParts) == 0 {
		return fmt.Errorf("empty command")
	}

	// If remote commands are completely disabled, reject
	if !p.AllowRemoteCommands {
		return fmt.Errorf("remote command execution is disabled for security")
	}

	// If allowlist is empty, allow all commands (unsafe but backward compatible)
	if len(p.AllowedCommands) == 0 {
		return nil
	}

	// Check if command is in allowlist
	cmd := cmdParts[0]
	for _, allowed := range p.AllowedCommands {
		// Exact match only - prevent prefix bypass attacks
		if cmd == allowed {
			return nil
		}
		// Also allow if the command is within an allowed directory path
		// e.g., allowed="/usr/bin/curl" matches cmd="/usr/bin/curl"
		// but does NOT match cmd="/usr/bin/curlfoo"
		if strings.HasPrefix(cmd, allowed+"/") {
			return nil
		}
	}

	return fmt.Errorf("command '%s' not in allowlist", cmd)
}

// DefaultSecurePolicy returns a policy that blocks all remote commands
func DefaultSecurePolicy() *CommandPolicy {
	return &CommandPolicy{
		AllowRemoteCommands: false,
		AllowedCommands:     []string{},
	}
}

// AllowlistPolicy returns a policy with specific allowed commands
func AllowlistPolicy(commands ...string) *CommandPolicy {
	return &CommandPolicy{
		AllowRemoteCommands: true,
		AllowedCommands:     commands,
	}
}
