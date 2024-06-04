package messaging

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (m *messaging) SetWithStruct(sync bool, messaging *structureSpec.Messaging) error {
	ops := make([]basic.Op, 0)
	var opMapper = common.Mapper{
		{"Id", false, func() error {
			ops = append(ops, Id(messaging.Id))
			return nil
		}},
		{"Description", false, func() error {
			ops = append(ops, Description(messaging.Description))
			return nil
		}},
		{"Tags", false, func() error {
			ops = append(ops, Tags(messaging.Tags))
			return nil
		}},
		{"Local", false, func() error {
			ops = append(ops, Local(messaging.Local))
			return nil
		}},
		{"SmartOps", true, func() error {
			ops = append(ops, SmartOps(messaging.SmartOps))
			return nil
		}},
	}

	err := opMapper.Run(messaging)
	if err != nil {
		return fmt.Errorf("mapping values failed with: %s", err)
	}

	ops = append(ops, Channel(messaging.Regex, messaging.Match))
	ops = append(ops, Bridges(messaging.MQTT, messaging.WebSocket))

	return m.Set(sync, ops...)
}
