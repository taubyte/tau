package service

import (
	"context"

	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/core/services/substrate/smartops"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

var _ smartops.EventCaller = &Service{}
var _ iface.ServiceResource = &Service{}

// For running smartOps of a messaging channel before running a function itself.
type Service struct {
	ctx         context.Context
	_type       uint32
	srv         iface.Service
	project     string
	application string
	config      *structureSpec.Service
}

func (s *Service) Type() uint32 {
	return s._type
}

func (s *Service) SmartOps(smartOps []string) (uint32, error) {
	return s.srv.SmartOps().Run(s, smartOps)
}

func (s *Service) Context() context.Context {
	return s.ctx
}

func (s *Service) Application() string {
	return s.application
}

func (s *Service) Project() string {
	return s.project
}

func (s *Service) Config() *structureSpec.Service {
	return s.config
}
