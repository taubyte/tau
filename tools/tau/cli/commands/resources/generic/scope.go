package generic

import (
	"fmt"

	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/urfave/cli/v2"
)

// A container kind holds resources of its own, which makes it a scope the CLI
// can be inside: selecting one narrows every other resource command to it.

func (l link) Select() common.Command {
	if !l.group.Container {
		return common.NotImplemented
	}
	return common.Create(&cli.Command{Flags: []cli.Flag{flags.None}, Action: l.selectScope})
}

func (l link) Clear() common.Command {
	if !l.group.Container {
		return common.NotImplemented
	}
	return common.Create(&cli.Command{Action: func(*cli.Context) error { return l.clearScope() }})
}

func (l link) selectScope(ctx *cli.Context) error {
	if ctx.IsSet(flags.Name.Name) && ctx.Bool(flags.None.Name) {
		return fmt.Errorf("cannot use --name and --none together")
	}
	if ctx.Bool(flags.None.Name) {
		return l.clearScope()
	}

	st, err := open()
	if err != nil {
		return err
	}
	name, _, err := st.Select(ctx, l.group)
	if err != nil {
		return err
	}
	return l.enterScope(name)
}

func (l link) enterScope(name string) error {
	if err := session.Set().SelectedApplication(name); err != nil {
		return err
	}
	l.success("Selected", name)
	return nil
}

func (l link) clearScope() error {
	if err := session.Unset().SelectedApplication(); err != nil {
		return err
	}
	printer.Out.SuccessPrintfln("Cleared %s selection", l.group.Name)
	return nil
}
