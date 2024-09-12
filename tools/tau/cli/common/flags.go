package common

import (
	"github.com/urfave/cli/v2"
)

type flagHandler struct {
	linker *linker
}

func (l *linker) Flags() FlagHandler {
	return flagHandler{l}
}

// Shift will add a flag to the start of the flag chain
func (h flagHandler) Shift(flags ...cli.Flag) {
	h.linker.Raw().Flags = append(flags, h.linker.Raw().Flags...)
}

// Push will add a flag to the end of the flag chain.
func (h flagHandler) Push(flags ...cli.Flag) {
	h.linker.Raw().Flags = append(h.linker.Raw().Flags, flags...)
}
