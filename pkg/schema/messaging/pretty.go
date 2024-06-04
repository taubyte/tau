package messaging

import "github.com/taubyte/tau/pkg/schema/pretty"

func (m *messaging) Prettify(pretty.Prettier) map[string]interface{} {
	getter := m.Get()
	return map[string]interface {
	}{
		"Id":           getter.Id(),
		"Name":         getter.Name(),
		"Description":  getter.Description(),
		"Tags":         getter.Tags(),
		"Local":        getter.Local(),
		"Regex":        getter.Regex(),
		"ChannelMatch": getter.ChannelMatch(),
		"MQTT":         getter.MQTT(),
		"WebSocket":    getter.WebSocket(),
	}
}
