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

	picks, err := s.Lookup(matcher)
	if err != nil {
		return fmt.Errorf("lookup failed with: %s", err.Error())
	}
	if len(picks) == 0 {
		return fmt.Errorf("lookup returned no picks")
	}

	ctx, ctxC := context.WithCancel(s.Context())
	workers := make(chan struct{}, 64)
	for range 64 {
		workers <- struct{}{}
	}

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

		go func() {
			defer func() {
				workers <- struct{}{}
			}()
			select {
			case <-workers:
				s.handle(start, matcher, message)
			case <-ctx.Done():
				return
			}
		}()
	}, func(err error) {
		common.Logger.Error("handle error with:", err.Error())
		ctxC()
	})

	if err != nil {
		common.Logger.Error("subscribe failed with:", err.Error())
		ctxC()
		return err
	}

	return nil
}
