package router

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	ce "github.com/taubyte/tau/p2p/streams/command/error"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

type CommandHandler func(context.Context, streams.Connection, command.Body) (cr.Response, error)
type StreamHandler func(context.Context, io.ReadWriter)

type handlers struct {
	std    CommandHandler
	stream StreamHandler
}

type Router struct {
	svr          *streams.StreamManger
	staticRoutes map[string]handlers
}

func New(svr *streams.StreamManger) *Router {
	return &Router{svr: svr, staticRoutes: map[string]handlers{}}
}

func (r *Router) AddStatic(command string, handler CommandHandler, stream StreamHandler) error {
	if handler == nil {
		return errors.New("router: cannot add nil handler")
	}

	if _, ok := r.staticRoutes[command]; ok {
		return fmt.Errorf("router: command %q already registered", command)
	}

	r.staticRoutes[command] = handlers{
		std:    handler,
		stream: stream,
	}
	return nil
}

func (r *Router) handle(cmd *command.Command) (cr.Response, StreamHandler, error) {
	if cmd == nil {
		return nil, nil, fmt.Errorf("router: received nil command")
	}

	conn, err := cmd.Connection()
	if err != nil {
		return nil, nil, fmt.Errorf("router: getting connection for command failed: %w", err)
	}

	if _handlers, ok := r.staticRoutes[cmd.Command]; ok {
		ret, err := _handlers.std(r.svr.Context(), conn, cmd.Body)
		if err != nil {
			return ret, _handlers.stream, fmt.Errorf("router: executing command %q failed: %w", cmd.Command, err)
		}
		return ret, _handlers.stream, err
	}

	return nil, nil, fmt.Errorf("router: command %q not registered", cmd.Command)
}

func (r *Router) Handle(s streams.Stream) {
	defer s.Close()

	c, err := command.Decode(s.Conn(), s)
	if err != nil {
		ce.Encode(s, fmt.Errorf("router: decoding command failed: %w", err))
		return
	}

	creturn, upgrade, err := r.handle(c)
	if err != nil {
		ce.Encode(s, err)
		return
	}

	err = creturn.Encode(s)
	if err != nil {
		ce.Encode(s, fmt.Errorf("router: encoding response failed: %w", err))
		return
	}

	if upgrade != nil {
		upgrade(r.svr.Context(), s)
	}
}
