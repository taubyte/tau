package pubsub

import (
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	"github.com/taubyte/odo/protocols/substrate/components/pubsub/common"
	"github.com/taubyte/odo/protocols/substrate/components/pubsub/websocket"
	counter "github.com/taubyte/odo/vm/counter"
)

type handleType int

const (
	handleTypeUnkown handleType = iota
	handleTypeMessage
	handleTypeError
)

func (s *Service) handle(startTime time.Time, matcher *common.MatchDefinition, _type handleType, data interface{}) {
	picks, err := s.Lookup(matcher)
	if err != nil {
		common.Logger.Errorf("lookup failed with: %w", err)
	}
	if len(picks) == 0 {
		common.Logger.Errorf("pick ==nil failed with err")

	}
	for _, pick := range picks {
		go func(_pick iface.Serviceable) {
			var err error
			var coldStartDone time.Time
			switch _type {
			case handleTypeMessage:
				coldStartDone, err = _pick.HandleMessage(data.(*pubsub.Message))
			case handleTypeError:
				err = data.(error)
			}
			if err != nil {
				counter.ErrorWrapper(_pick, startTime, coldStartDone, err)
				common.Logger.Errorf("Handling message failed with: %w", err)
			}
		}(pick)
	}
}

// TODO smartops and cache the serviceable
func (s *Service) Subscribe(projectId, appId, path string) error {
	start := time.Now()
	matcher := &common.MatchDefinition{
		Channel:     path,
		Project:     projectId,
		Application: appId,
	}

	if matcher.Channel[0] == '/' {
		matcher.Channel = matcher.Channel[1:]
	}

	_, err := websocket.AddSubscription(s, matcher.Path(), func(msg *pubsub.Message) {
		s.handle(start, matcher, handleTypeMessage, msg)
	}, func(err error) {
		s.handle(start, matcher, handleTypeError, err)
	})

	if err != nil {
		common.Logger.Errorf("subscribe failed with err: %w", err)
	}

	return err
}
