package functions

import (
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (g getter) Struct() (fun *structureSpec.Function, err error) {
	timeout, err := common.StringToTime(g.Timeout())
	if err != nil {
		return nil, err
	}

	memory, err := common.StringToUnits(g.Memory())
	if err != nil {
		return nil, err
	}

	_type := g.Type()
	fun = &structureSpec.Function{
		Id:          g.Id(),
		Name:        g.Name(),
		Description: g.Description(),
		Tags:        g.Tags(),
		Type:        _type,
		Timeout:     timeout,
		Memory:      memory,
		Call:        g.Call(),
		Source:      g.Source(),
		SmartOps:    g.SmartOps(),
	}

	switch _type {
	case "http", "https":
		fun.Domains = g.Domains()
		fun.Method = g.Method()
		fun.Paths = g.Paths()
		fun.Secure = _type == "https"
	case "p2p":
		fun.Protocol = g.Protocol()
		fun.Command = g.Command()
		fun.Local = g.Local()
	case "pubsub":
		fun.Channel = g.Channel()
		fun.Local = g.Local()
	}

	return
}
