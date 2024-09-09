package website

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
)

func (l link) Clone() common.Command {
	return l.cmd.CloneCmd()
}
