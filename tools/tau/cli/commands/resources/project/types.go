package project

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
)

type link struct {
	common.UnimplementedBasic
}

func New() common.Basic {
	return link{}
}
