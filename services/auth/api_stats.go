package auth

import (
	"context"
	"errors"

	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (srv *AuthService) statsServiceHandler(ctx context.Context, st streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	switch action {
	case "db":
		return cr.Response{"stats": srv.db.Stats().Encode()}, nil
	default:
		return nil, errors.New("stats action `" + action + "` not recognized")
	}
}
