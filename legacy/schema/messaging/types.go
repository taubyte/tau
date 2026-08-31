package messaging

import (
	"github.com/taubyte/tau/legacy/schema/basic"
	"github.com/taubyte/tau/legacy/schema/common"
	structureSpec "github.com/taubyte/tau/legacy/specs/structure"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

type Messaging interface {
	Get() Getter
	common.Resource[*structureSpec.Messaging]
}

type messaging struct {
	*basic.Resource
	seer        *seer.Seer
	name        string
	application string
}

type Getter interface {
	basic.ResourceGetter[*structureSpec.Messaging]
	Local() bool
	Regex() bool
	ChannelMatch() string
	MQTT() bool
	WebSocket() bool
}
