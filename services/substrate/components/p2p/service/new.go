package service

import (
	"context"

	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func New(ctx context.Context, _type uint32, srv iface.Service, project, application string, config *structureSpec.Service) (*Service, error) {
	return &Service{ctx, _type, srv, project, application, config}, nil
}
