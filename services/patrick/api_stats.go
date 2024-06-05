package service

import (
	"context"
	"errors"

	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (p *PatrickService) statsServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	switch action {
	case "db":
		return cr.Response{"stats": p.db.Stats().Encode()}, nil
	default:
		return nil, errors.New("stats action `" + action + "` not recognized")
	}
}
