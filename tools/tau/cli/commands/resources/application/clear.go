package application

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	applicationI18n "github.com/taubyte/tau/tools/tau/i18n/application"
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
	if err := session.Unset().SelectedApplication(); err != nil {
		return err
	}
	applicationI18n.ClearedApplicationSelection()
	return nil
}
