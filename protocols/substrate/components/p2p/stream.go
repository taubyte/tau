package p2p

import (
	"errors"
	"fmt"
	"time"

	"github.com/mitchellh/mapstructure"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	"github.com/taubyte/odo/protocols/substrate/components/p2p/common"
	counter "github.com/taubyte/odo/vm/counter"
	"github.com/taubyte/odo/vm/lookup"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	ce "github.com/taubyte/p2p/streams/command/error"
	"github.com/taubyte/p2p/streams/command/response"
	"github.com/taubyte/p2p/streams/command/router"
)

type commandService struct {
	name   string
	peer   peer.Node
	router *(router.Router)
	stream *(streams.StreamManger)
}

func (cs *commandService) Close() {
	cs.stream.Stop()
}

func (srv *Service) StartStream(name, protocol string, handler iface.StreamHandler) (iface.CommandService, error) {
	var cs commandService
	cs.name = name
	cs.peer = srv.Node()

	cs.stream = streams.New(srv.Node(), name, protocol)
	if cs.stream == nil {
		return nil, errors.New("not able to create service")
	}

	cs.router = router.New(cs.stream, nil)
	if cs.router == nil {
		return nil, errors.New("not able to create command router")
	}

	cs.stream.Start(func(s streams.Stream) {
		defer s.Close()

		c, err := command.Decode(s.Conn(), s)
		if err != nil {
			err1 := ce.Encode(s, err)
			if err1 != nil {
				common.Logger.Errorf("ce.Encode1- failed with: %w", err1)
				return
			}
			return
		}

		creturn, err := handler(c)
		if err != nil {
			err1 := ce.Encode(s, err)
			if err1 != nil {
				common.Logger.Errorf("ce.Encode-2 failed with: %w", err1)
				return
			}
			return
		}

		err = creturn.Encode(s)
		if err != nil {
			common.Logger.Errorf("ce.Encode-3 failed with: %w", err)
			return
		}
	})
	return &cs, nil
}

// Handles calls made with sdk
func (s *Service) Handle(cmd *command.Command) (resp response.Response, err error) {
	start := time.Now()
	_matcher, ok := cmd.Get("matcher")
	if !ok {
		return nil, fmt.Errorf("matcher not found in command")
	}

	var matcher *iface.MatchDefinition
	if err = mapstructure.Decode(_matcher, &matcher); err != nil {
		return nil, fmt.Errorf("decoding matcher failed with: %s", err.Error())
	}

	pickServiceables, err := lookup.Lookup(s, matcher)
	if err != nil {
		return nil, fmt.Errorf("p2P serviceable lookup failed with: %s", err)
	}

	if len(pickServiceables) > 1 {
		return nil, fmt.Errorf("unexpected multiple picks for given matcher %v", matcher)
	}

	pick, ok := pickServiceables[0].(iface.Serviceable)
	if !ok {
		return nil, fmt.Errorf("matched serviceable is not a P2P serviceable")
	}

	if _, ok = cmd.Get("data"); !ok {
		return nil, counter.ErrorWrapper(pick, start, time.Time{}, fmt.Errorf("missing data is body %v", cmd.Raw()))
	}
	// Set/delete relative values
	cmd.Delete("matcher")
	cmd.Set("command", matcher.Command)
	cmd.Set("protocol", matcher.Protocol)

	if err := pick.Ready(); err != nil {
		return nil, counter.ErrorWrapper(pick, start, time.Time{}, fmt.Errorf("p2p stream serviceable is not ready with: %s", err))
	}

	coldStartDone, response, err := pick.Handle(cmd)
	return response, counter.ErrorWrapper(pick, start, coldStartDone, err)
}
