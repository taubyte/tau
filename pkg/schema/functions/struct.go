package functions

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (f *function) SetWithStruct(sync bool, function *structureSpec.Function) error {
	ops := make([]basic.Op, 0)
	var opMapper = common.Mapper{
		{"Id", false, func() error {
			ops = append(ops, Id(function.Id))
			return nil
		}},
		{"Description", false, func() error {
			ops = append(ops, Description(function.Description))
			return nil
		}},
		{"Tags", false, func() error {
			ops = append(ops, Tags(function.Tags))
			return nil
		}},
		{"Type", true, func() error {
			ops = append(ops, Type(function.Type))
			return nil
		}},
		{"Local", false, func() error {
			switch function.Type {
			case "pubsub", "p2p":
				ops = append(ops, Local(function.Local))
			}
			return nil
		}},
		{"Command", true, func() error {
			switch function.Type {
			case "p2p":
				ops = append(ops, Command(function.Command))
			}
			return nil
		}},
		{"Service", true, func() error {
			switch function.Type {
			case "p2p":
				ops = append(ops, Protocol(function.Protocol))
			}
			return nil
		}},
		{"Channel", true, func() error {
			switch function.Type {
			case "pubsub":
				ops = append(ops, Channel(function.Channel))
			}
			return nil
		}},
		{"Method", true, func() error {
			switch function.Type {
			case "pubsub", "p2p":
			default:
				ops = append(ops, Method(function.Method))
			}
			return nil
		}},
		{"Paths", true, func() error {
			switch function.Type {
			case "pubsub", "p2p":
			default:
				ops = append(ops, Paths(function.Paths))
			}
			return nil
		}},
		{"Timeout", true, func() error {
			ops = append(ops, Timeout(common.TimeToString(function.Timeout)))
			return nil
		}},
		{"Memory", true, func() error {
			ops = append(ops, Memory(common.UnitsToString(function.Memory)))
			return nil
		}},
		{"Call", true, func() error {
			ops = append(ops, Call(function.Call))
			return nil
		}},
		{"Source", false, func() error {
			ops = append(ops, Source(function.Source))
			return nil
		}},
		{"Domains", true, func() error {
			ops = append(ops, Domains(function.Domains))
			return nil
		}},
		{"SmartOps", true, func() error {
			ops = append(ops, SmartOps(function.SmartOps))
			return nil
		}},
	}

	err := opMapper.Run(function)
	if err != nil {
		return fmt.Errorf("mapping values failed with: %s", err)
	}

	return f.Set(sync, ops...)
}
