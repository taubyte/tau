package service

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/go-specs/structure"
)

func New(ctx context.Context, _type uint32, srv iface.Service, project, application string, config *structureSpec.Service) (*Service, error) {
	return &Service{ctx, _type, srv, project, application, config}, nil
}
