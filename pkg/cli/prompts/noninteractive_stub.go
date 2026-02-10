//go:build noPrompt

package prompts

import "errors"

func IsNonInteractive() bool {
	return false
}

var ErrNonInteractive = errors.New("cannot prompt: non-interactive mode")
