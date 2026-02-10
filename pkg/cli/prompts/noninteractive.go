//go:build !noPrompt

package prompts

import (
	"errors"
	"os"

	"golang.org/x/term"
)

// NonInteractiveEnvVar is set (e.g. to "1") when the process should not use interactive prompts.
const NonInteractiveEnvVar = "TAU_NON_INTERACTIVE"

// IsNonInteractive reports whether the CLI should avoid interactive prompts (TTY or env).
// When true, callers should return ErrNonInteractive or a wrapped error instead of prompting.
func IsNonInteractive() bool {
	if os.Getenv(NonInteractiveEnvVar) != "" || os.Getenv("CI") != "" {
		return true
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return true
	}
	return false
}

// ErrNonInteractive is returned when a prompt would run but the environment is non-interactive.
// Callers should suggest using flags (e.g. "use --name, --provider, --token for login").
var ErrNonInteractive = errors.New("cannot prompt: non-interactive mode (stdin not a TTY or " + NonInteractiveEnvVar + "/CI set); use command flags instead")
