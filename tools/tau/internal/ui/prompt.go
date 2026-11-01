package ui

import (
	"io"

	"github.com/charmbracelet/huh"
)

// Prompter asks the developer for what a command was not given. A command only
// holds one when its input is a terminal; otherwise a missing value is an error
// naming the flag that supplies it.
type Prompter interface {
	Input(title, description string, validate func(string) error) (string, error)
	Secret(title, description string) (string, error)
	Confirm(title string) (bool, error)
	Select(title string, options []string) (string, error)
}

// Huh is the Prompter over charmbracelet/huh forms. In and Out default to the
// terminal; Accessible is huh's line-by-line mode, for screen readers (and
// TAU_ACCESSIBLE=1).
type Huh struct {
	In         io.Reader
	Out        io.Writer
	Accessible bool
}

func (h Huh) run(field huh.Field) error {
	form := huh.NewForm(huh.NewGroup(field)).WithAccessible(h.Accessible)
	if h.In != nil {
		form = form.WithInput(h.In)
	}
	if h.Out != nil {
		form = form.WithOutput(h.Out)
	}
	return form.Run()
}

func (h Huh) Input(title, description string, validate func(string) error) (string, error) {
	var v string
	in := huh.NewInput().Title(title).Description(description).Value(&v)
	if validate != nil {
		in = in.Validate(validate)
	}
	return v, h.run(in)
}

// Secret asks for a value without echoing it.
func (h Huh) Secret(title, description string) (string, error) {
	var v string
	return v, h.run(huh.NewInput().Title(title).Description(description).EchoMode(huh.EchoModePassword).Value(&v))
}

func (h Huh) Confirm(title string) (bool, error) {
	var v bool
	return v, h.run(huh.NewConfirm().Title(title).Value(&v))
}

func (h Huh) Select(title string, options []string) (string, error) {
	var v string
	return v, h.run(huh.NewSelect[string]().Title(title).Options(huh.NewOptions(options...)...).Value(&v))
}
