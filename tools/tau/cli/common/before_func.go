package common

import "github.com/urfave/cli/v2"

type beforeHandler struct {
	linker *linker
}

func (l *linker) Before() BeforeHandler {
	return beforeHandler{l}
}

// Shift will add a before function to the start of the before chain.
func (h beforeHandler) Shift(method cli.BeforeFunc) {
	prev := h.linker.Raw().Before

	if prev != nil {
		method = func(ctx *cli.Context) error {
			err := prev(ctx)
			if err != nil {
				return err
			}

			return method(ctx)
		}
	}

	h.linker.Raw().Before = method
}

// Push will add a before function to the end of the before chain.
func (h beforeHandler) Push(method cli.BeforeFunc) {
	prev := h.linker.Raw().Before

	if prev != nil {
		method = func(ctx *cli.Context) error {
			err := method(ctx)
			if err != nil {
				return err
			}

			return prev(ctx)
		}
	}

	h.linker.Raw().Before = method
}
