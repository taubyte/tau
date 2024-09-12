package common

import (
	"github.com/urfave/cli/v2"
)

func newBaseCommand(name string) *cli.Command {
	return &cli.Command{
		Name:        name,
		Subcommands: make([]*cli.Command, 0),
	}
}

var (
	_new      = newBaseCommand("new")
	_edit     = newBaseCommand("edit")
	_delete   = newBaseCommand("delete")
	_query    = newBaseCommand("query")
	_list     = newBaseCommand("list")
	_select   = newBaseCommand("select")
	_clone    = newBaseCommand("clone")
	_push     = newBaseCommand("push")
	_pull     = newBaseCommand("pull")
	_checkout = newBaseCommand("checkout")
	_import   = newBaseCommand("import")
)
