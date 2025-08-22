package smartops

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/common"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func (s *smartOps) WrapError(format string, i ...any) error {
	return fmt.Errorf("on smartops `"+s.name+"`; "+format, i...)
}

func (s *smartOps) Root() *seer.Query {
	return s.Resource.Root()
}

func (s *smartOps) Config() *seer.Query {
	return s.Resource.Config()
}

func (s *smartOps) Name() string {
	return s.name
}

func (s *smartOps) AppName() string {
	return s.application
}

func (s *smartOps) Directory() string {
	return common.SmartOpsFolder
}

func (s *smartOps) SetName(name string) {
	s.name = name
}
