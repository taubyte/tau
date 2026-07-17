package messaging

import (
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/basic"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/tcc/internal/parity/specs/structure"
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
