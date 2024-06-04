package messaging

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (g getter) Struct() (msg *structureSpec.Messaging, err error) {
	msg = &structureSpec.Messaging{
		Id:          g.Id(),
		Name:        g.Name(),
		Description: g.Description(),
		Tags:        g.Tags(),
		Local:       g.Local(),
		Match:       g.ChannelMatch(),
		Regex:       g.Regex(),
		MQTT:        g.MQTT(),
		WebSocket:   g.WebSocket(),
		SmartOps:    g.SmartOps(),
	}

	return
}
