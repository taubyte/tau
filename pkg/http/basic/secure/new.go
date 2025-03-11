package secure

import (
	"context"

	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"
)

func New(ctx context.Context, opts ...options.Option) (*Service, error) {
	_s, err := basicHttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	var s Service
	s.Service = _s

	err = options.Parse(&s, opts)
	if err != nil {
		return nil, err
	}

	return &s, nil
}
