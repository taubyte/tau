package messaging

import (
	"fmt"

	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/common"
)

func (m *messaging) WrapError(format string, i ...any) error {
	return fmt.Errorf("on messaging `"+m.name+"`; "+format, i...)
}

func (m *messaging) Root() *seer.Query {
	return m.Resource.Root()
}

func (m *messaging) Config() *seer.Query {
	return m.Resource.Config()
}

func (m *messaging) Name() string {
	return m.name
}

func (m *messaging) AppName() string {
	return m.application
}

func (*messaging) Directory() string {
	return common.MessagingFolder
}

func (m *messaging) SetName(name string) {
	m.name = name
}
