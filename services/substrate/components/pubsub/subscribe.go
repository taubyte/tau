package pubsub

import (
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
			fmt.Printf("SERVICE message %p >>>>> %s\n", msg, string(msg.GetData()))
			coldStartDone, err := _pick.HandleMessage(msg)
			if err != nil {
				counter.ErrorWrapper(_pick, startTime, coldStartDone, err)
				common.Logger.Error("Handling message failed with:", err.Error())
			}
		}(pick)
	}
	waitGroup.Wait()
}

// TODO smartops and cache the serviceable
func (s *Service) Subscribe(projectId, appId, resource, path string) error {
	fmt.Println("SUBSCRIBING>>>>>", projectId, appId, resource, path)
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
		fmt.Println("LOOKUP FAILED>>>>>", err.Error())
		return fmt.Errorf("lookup failed with: %s", err.Error())
	}
	if len(picks) == 0 {
		fmt.Println("LOOKUP RETURNED NO PICKS>>>>>")
		return fmt.Errorf("lookup returned no picks")
	}

	_, err = websocket.AddSubscription(s, matcher.String(), func(msg *pubsub.Message) {
		// unwarp the message first
		fmt.Println("GOT FUNC message>>>>>", string(msg.Data))
		message, err := common.NewMessage(msg, "")
		if err != nil {
			common.Logger.Errorf("Creating message failed with: %v", err)
			return
		}
		if message.GetSource() == resource {
			// ignore the message - comes from self
			fmt.Println("GOT message source is self>>>>>", message.GetSource())
			return
		}
		fmt.Println("GOT message source is not self>>>>>", message.GetSource())

		// the try to handle the message
		s.handle(start, matcher, message)
	}, func(err error) {
		common.Logger.Error("handle error with:", err.Error())
	})

	if err != nil {
		common.Logger.Error("subscribe failed with:", err.Error())
	}

	return err
}
