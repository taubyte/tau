package common

import (
	"fmt"

	multihash "github.com/taubyte/tau/utils/multihash"
)

func (m *MatchDefinition) Path() string {
	return fmt.Sprintf("%s/%s", multihash.Hash(m.Project+m.Application), m.Channel)
}

func (m *MatchDefinition) GenerateSocketURL() string {
	return fmt.Sprintf(WebSocketFormat, multihash.Hash(m.Project+m.Application), m.Channel)
}
