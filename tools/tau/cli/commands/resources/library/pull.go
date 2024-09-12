package library

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
)

func (l link) Pull() common.Command {
	return l.cmd.PullCmd()
}
