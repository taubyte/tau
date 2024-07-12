package router

import (
	"context"
	"errors"
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
		return errors.New("can not add nil handler")
	}

	if _, ok := r.staticRoutes[command]; ok {
		return errors.New("Command `" + command + "` already exists.")
	}

	r.staticRoutes[command] = handlers{
		std:    handler,
		stream: stream,
	}
	return nil
}

func (r *Router) handle(cmd *command.Command) (cr.Response, StreamHandler, error) {
	if cmd == nil {
		return nil, nil, errors.New("empty command")
	}

	conn, err := cmd.Connection()
	if err != nil {
		return nil, nil, err
	}

	if _handlers, ok := r.staticRoutes[cmd.Command]; ok {
		ret, err := _handlers.std(r.svr.Context(), conn, cmd.Body)
		return ret, _handlers.stream, err
	}

	return nil, nil, errors.New("command `" + cmd.Command + "` does not exist.")
}

func (r *Router) Handle(s streams.Stream) {
	defer s.Close()

	c, err := command.Decode(s.Conn(), s)
	if err != nil {
		ce.Encode(s, err)
		return
	}

	creturn, upgrade, err := r.handle(c)
	if err != nil {
		ce.Encode(s, err)
		return
	}

	err = creturn.Encode(s)
	if err != nil {
		ce.Encode(s, err)
		return
	}

	if upgrade != nil {
		upgrade(r.svr.Context(), s)
	}
}
