package smartops

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (s *smartOps) SetWithStruct(sync bool, smartOp *structureSpec.SmartOp) error {
	ops := make([]basic.Op, 0)
	var opMapper = common.Mapper{
		{"Id", false, func() error {
			ops = append(ops, Id(smartOp.Id))
			return nil
		}},
		{"Description", false, func() error {
			ops = append(ops, Description(smartOp.Description))
			return nil
		}},
		{"Tags", false, func() error {
			ops = append(ops, Tags(smartOp.Tags))
			return nil
		}},
		{"Timeout", true, func() error {
			ops = append(ops, Timeout(common.TimeToString(smartOp.Timeout)))
			return nil
		}},
		{"Memory", true, func() error {
			ops = append(ops, Memory(common.UnitsToString(smartOp.Memory)))
			return nil
		}},
		{"Call", true, func() error {
			ops = append(ops, Call(smartOp.Call))
			return nil
		}},
		{"source", false, func() error {
			ops = append(ops, Source(smartOp.Source))
			return nil
		}},
	}

	err := opMapper.Run(smartOp)
	if err != nil {
		return fmt.Errorf("mapping values failed with: %s", err)
	}

	return s.Set(sync, ops...)
}
