package smartops

import (
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (g getter) Struct() (smartOp *structureSpec.SmartOp, err error) {
	timeout, err := common.StringToTime(g.Timeout())
	if err != nil {
		return nil, err
	}

	memory, err := common.StringToUnits(g.Memory())
	if err != nil {
		return nil, err
	}

	smartOp = &structureSpec.SmartOp{
		Id:          g.Id(),
		Name:        g.Name(),
		Description: g.Description(),
		Tags:        g.Tags(),
		Timeout:     timeout,
		Memory:      memory,
		Call:        g.Call(),
		Source:      g.Source(),
		SmartOps:    g.SmartOps(),
	}

	return
}
