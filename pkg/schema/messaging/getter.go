package messaging

import (
	"github.com/taubyte/tau/pkg/schema/basic"
)

type getter struct {
	*messaging
}

func (m *messaging) Get() Getter {
	return getter{m}
}

func (g getter) Name() string {
	return g.name
}

func (g getter) Application() string {
	return g.application
}

func (g getter) Id() string {
	return basic.Get[string](g, "id")
}

func (g getter) Description() string {
	return basic.Get[string](g, "description")
}

func (g getter) Tags() []string {
	return basic.Get[[]string](g, "tags")
}

func (g getter) Local() bool {
	return basic.Get[bool](g, "local")
}

func (g getter) ChannelMatch() string {
	return basic.Get[string](g, "channel", "match")
}

func (g getter) Regex() bool {
	return basic.Get[bool](g, "channel", "regex")
}

func (g getter) MQTT() bool {
	return basic.Get[bool](g, "bridges", "mqtt", "enable")
}

func (g getter) WebSocket() bool {
	return basic.Get[bool](g, "bridges", "websocket", "enable")
}

func (g getter) SmartOps() (value []string) {
	return basic.Get[[]string](g, "smartops")
}
