package services

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (s *service) SetWithStruct(sync bool, service *structureSpec.Service) error {
	ops := make([]basic.Op, 0)
	var opMapper = common.Mapper{
		{"Id", false, func() error {
			ops = append(ops, Id(service.Id))
			return nil
		}},
		{"Description", false, func() error {
			ops = append(ops, Description(service.Description))
			return nil
		}},
		{"Tags", false, func() error {
			ops = append(ops, Tags(service.Tags))
			return nil
		}},
		{"Protocol", false, func() error {
			ops = append(ops, Protocol(service.Protocol))
			return nil
		}},
		{"SmartOps", true, func() error {
			ops = append(ops, SmartOps(service.SmartOps))
			return nil
		}},
	}

	err := opMapper.Run(service)
	if err != nil {
		return fmt.Errorf("mapping values failed with: %s", err)
	}

	return s.Set(sync, ops...)
}
