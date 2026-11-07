package pubsub

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

// MaxConcurrentMessages bounds the messages one subscription handles at once;
// each holds a VM instance. Messages beyond it are dropped.
var MaxConcurrentMessages = 64

// dispatch runs fn in a goroutine when a worker slot is free and reports
// whether it did. It never waits: a blocked handler must not pile up
// goroutines and VM instances behind it.
func dispatch(workers chan struct{}, fn func()) bool {
	select {
	case workers <- struct{}{}:
	default:
		return false
	}
	go func() {
		defer func() { <-workers }()
		fn()
	}()
	return true
}

var lastDropWarn atomic.Int64

// warnDropped logs a dropped message at most once every 10 seconds.
func warnDropped(channel string) {
	now := time.Now().UnixNano()
	last := lastDropWarn.Load()
	if now-last < int64(10*time.Second) || !lastDropWarn.CompareAndSwap(last, now) {
		return
	}
	common.Logger.Warnf("dropping pubsub messages on %s: %d handlers already busy", channel, MaxConcurrentMessages)
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
	workers := make(chan struct{}, MaxConcurrentMessages)

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

		if ctx.Err() != nil {
			return
		}
		if !dispatch(workers, func() { s.handle(start, matcher, message) }) {
			warnDropped(matcher.Channel)
		}
	}, func(err error) {
		common.Logger.Error("handle error with:", err.Error())
		ctxC()
		s.subscribed.Delete(key)
	})

	if err != nil {
		common.Logger.Error("subscribe failed with:", err.Error())
		ctxC()
		s.subscribed.Delete(key)
		return err
	}

	return nil
}
