package pubsub

import (
	"context"
	"fmt"
	"sync"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"github.com/taubyte/tau/services/substrate/components/pubsub/websocket"
	counter "github.com/taubyte/tau/services/substrate/runtime/counter"
)

func (s *Service) handle(startTime time.Time, matcher *common.MatchDefinition, msg iface.Message) {
	picks, err := s.Lookup(matcher)
	if err != nil {
		common.Logger.Error("lookup failed with:", err.Error())
		return
	}
	if len(picks) == 0 {
		common.Logger.Error("lookup returned no picks")
		return
	}

	var waitGroup sync.WaitGroup
	waitGroup.Add(len(picks))
	for _, pick := range picks {
		go func(_pick iface.Serviceable) {
			defer waitGroup.Done()
			coldStartDone, err := _pick.HandleMessage(msg)
			if err != nil {
				counter.ErrorWrapper(_pick, startTime, coldStartDone, err)
				common.Logger.Error("Handling message failed with:", err.Error())
			}
		}(pick)
	}
	waitGroup.Wait()
}

func (s *Service) Subscribe(projectId, appId, resource, path string) error {
	start := time.Now()
	matcher := &common.MatchDefinition{
		Channel:     path,
		Project:     projectId,
		Application: appId,
	}

	if matcher.Channel[0] == '/' {
		matcher.Channel = matcher.Channel[1:]
	}

	// A function that subscribes on every call must not add a handler per
	// call: each one handles every message again.
	key := matcher.String() + "/" + resource
	if _, dup := s.subscribed.LoadOrStore(key, struct{}{}); dup {
		return nil
	}

	picks, err := s.Lookup(matcher)
	if err != nil {
		s.subscribed.Delete(key)
		return fmt.Errorf("lookup failed with: %s", err.Error())
	}
	if len(picks) == 0 {
		s.subscribed.Delete(key)
		return fmt.Errorf("lookup returned no picks")
	}

	ctx, ctxC := context.WithCancel(s.Context())
	q := newQueue(ctx, Function{projectId, appId, resource}, func(m iface.Message) { s.handle(start, matcher, m) })
	q.report = counterReport(s.Counter(), q.fn)

	_, err = websocket.AddSubscription(s, matcher.String(), func(msg *pubsub.Message) {
		// unwarp the message first
		message, err := common.NewMessage(msg, "")
		if err != nil {
			common.Logger.Errorf("Creating message failed with: %v", err)
			return
		}
		if message.GetSource() == resource {
			// ignore the message - comes from self
			return
		}

		q.push(message)
	}, func(err error) {
		common.Logger.Error("handle error with:", err.Error())
		ctxC()
		q.close()
		s.subscribed.Delete(key)
	})

	if err != nil {
		common.Logger.Error("subscribe failed with:", err.Error())
		ctxC()
		q.close()
		s.subscribed.Delete(key)
		return err
	}

	return nil
}
