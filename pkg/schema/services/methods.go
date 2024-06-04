package services

import (
	"fmt"

	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/common"
)

func (s *service) WrapError(format string, i ...any) error {
	return fmt.Errorf("on service `"+s.name+"`; "+format, i...)
}

func (s *service) Name() string {
	return s.name
}

func (s *service) Root() *seer.Query {
	return s.Resource.Root()
}

func (s *service) Config() *seer.Query {
	return s.Resource.Config()
}

func (s *service) AppName() string {
	return s.application
}

func (s *service) Directory() string {
	return common.ServiceFolder
}

func (s *service) SetName(name string) {
	s.name = name
}
