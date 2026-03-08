package project

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/urfave/cli/v2"
)

func (link) Clear() common.Command {
	return common.Create(
		&cli.Command{
			Action: _clear,
		},
	)
}

func _clear(ctx *cli.Context) error {
	if err := session.Unset().SelectedProject(); err != nil {
		return err
	}
	if err := session.Unset().SelectedApplication(); err != nil {
		return err
	}
	projectI18n.ClearedProjectSelection()
	return nil
}
