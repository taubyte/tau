package websocket

import (
	"context"
	"errors"
	"fmt"

	pubsub "github.com/libp2p/go-libp2p-pubsub"

	p2p "github.com/taubyte/tau/p2p/peer"
	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/specs/extract"
	messagingSpec "github.com/taubyte/tau/pkg/specs/messaging"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

var subs = &subsViewer{
	subscriptions: make(map[string]*subViewer),
}

func Handler(srv common.LocalService, ctx service.Context, conn service.WebSocketConnection) service.WebSocketHandler {
	conn.EnableWriteCompression(true)
	handler, err := createWsHandler(srv, ctx, conn)
	if err != nil {
		conn.WriteJSON(WrappedMessage{
			Error: fmt.Sprintf("Creating handler failed with: %v", err),
		})
		conn.Close()

		return nil
	}

	id, err := AddSubscription(srv, handler.matcher.String(), func(msg *pubsub.Message) {
		select {
		case <-handler.ctx.Done():
		case handler.ch <- msg.GetData():
		default:
		}
	}, func(err error) {
		common.Logger.Errorf("Add subscription to `%s` failed with %s", handler.matcher, err.Error())
		if handler.ctx.Err() == nil {
			select {
			case <-handler.ctx.Done():
			case handler.errCh <- err:
			default:
			}
		}
	})
	if err != nil {
		conn.Close()
		handler.Close()
		return nil
	}

	conn.SetCloseHandler(func(code int, text string) error {
		removeSubscription(handler.matcher.String(), id)
		handler.Close()
		return nil
	})

	return handler
}

func (sv *subViewer) getNextId() int {
	ret := sv.nextId
	sv.nextId++
	return ret
}

func (sv *subViewer) handler(msg *pubsub.Message) {
	sv.Lock()
	defer sv.Unlock()
	// Process subscriptions sequentially to avoid goroutine explosion
	for _, subscription := range sv.subs {
		subscription.handler(msg)
	}
}

func (sv *subViewer) err_handler(err error) {
	sv.Lock()
	defer sv.Unlock()
	// Process subscriptions sequentially to avoid goroutine explosion
	for _, subscription := range sv.subs {
		subscription.err_handler(err)
	}
}

func removeSubscription(name string, subIdx int) {
	subs.Lock()
	defer subs.Unlock()
	subset, ok := subs.subscriptions[name]
	if !ok {
		return
	}

	_, ok = subset.subs[subIdx]
	if !ok {
		return
	}

	delete(subset.subs, subIdx)
}

func AddSubscription(srv common.LocalService, name string, handler p2p.PubSubConsumerHandler, err_handler p2p.PubSubConsumerErrorHandler) (subIdex int, err error) {
	// TODO: this block should be its own function for lock/unlock
	subs.Lock()
	defer subs.Unlock()
	subset, ok := subs.subscriptions[name]
	if !ok {
		subset = new(subViewer)
		subset.subs = make(map[int]*sub, 0)
		err = srv.Node().PubSubSubscribe(name, subset.handler, subset.err_handler)
	}

	// Catching error outside so the unlock can happen right away for the inner lock
	// to take over.
	if err != nil {
		return 0, fmt.Errorf("pubsub subscribe failed with: %w", err)
	}

	subset.Lock()
	defer subset.Unlock()

	newId := subset.getNextId()
	subset.subs[newId] = &sub{
		handler:     handler,
		err_handler: err_handler,
	}

	subs.subscriptions[name] = subset

	return newId, nil
}

func createWsHandler(srv common.LocalService, ctx service.Context, conn service.WebSocketConnection) (*dataStreamHandler, error) {
	hash, err := ctx.GetStringVariable("hash")
	if err != nil {
		return nil, fmt.Errorf("getting string variable `hash` failed with %w", err)
	}

	webSocketPath, err := messagingSpec.Tns().WebSocketPath(hash)
	if err != nil {
		return nil, fmt.Errorf("getting websocket path from hash `%s` failed with: %w", hash, err)
	}

	ifacePaths, err := srv.Tns().Fetch(webSocketPath)
	if err != nil {
		return nil, fmt.Errorf("fetching web socket path `%s` failed with: %w", webSocketPath, err)
	}

	fetchPaths, ok := ifacePaths.Interface().([]interface{})
	if !ok {
		return nil, errors.New("no valid connections found")
	}

	var projectId, applicationId string
	for _, ifacePath := range fetchPaths {
		_path, ok := ifacePath.(string)
		if !ok {
			return nil, fmt.Errorf("path not of type string `%T`: `%s`", ifacePath, ifacePath)
		}

		parser, err := extract.Tns().BasicPath(_path)
		if err != nil {
			return nil, fmt.Errorf("tns path regex check for `%s` failed with: %w", _path, err)
		}

		projectId = parser.Project()
		applicationId = parser.Application()
	}

	channel, err := ctx.GetStringVariable("channel")
	if err != nil {
		return nil, fmt.Errorf("getting channel variable failed with: %w", err)
	}

	matcher := &common.MatchDefinition{
		Channel:     channel,
		Project:     projectId,
		Application: applicationId,
		WebSocket:   true,
	}

	serviceables, err := srv.Lookup(matcher)
	if err != nil {
		return nil, err
	}
	if len(serviceables) == 0 {
		return nil, errors.New("no serviceables found")
	}

	handler := new(dataStreamHandler)
	handler.ctx, handler.ctxC = context.WithCancel(srv.Context())
	handler.conn = conn
	handler.matcher = matcher
	handler.srv = srv
	handler.ch = make(chan []byte)
	handler.errCh = make(chan error)

	return handler, nil
}
