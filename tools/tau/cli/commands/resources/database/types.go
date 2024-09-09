package database

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
)

type link struct {
	common.UnimplementedBasic
}

// New is called in tau/cli/new.go to attach the relative commands
// to their parents, i.e `new` => `new database`
func New() common.Basic {
	return link{}
}
