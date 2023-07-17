package pubsub

import (
	"errors"
	"fmt"

	"bitbucket.org/taubyte/go-node-tvm/lookup"
	moody "github.com/taubyte/go-interfaces/moody"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/common"
	iface "github.com/taubyte/go-interfaces/services/substrate/pubsub"
	functionSpec "github.com/taubyte/go-specs/function"
	matcherSpec "github.com/taubyte/go-specs/matcher"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
	"github.com/taubyte/odo/protocols/node/components/pubsub/function"
	"github.com/taubyte/odo/protocols/node/components/pubsub/websocket"
)

var (
	TheServiceables = []string{string(functionSpec.PathVariable)}
)

func (s *Service) Lookup(matcher *common.MatchDefinition) ([]iface.Serviceable, error) {
	serviceables, err := lookup.Lookup(s, matcher)
	if err != nil {
		return nil, fmt.Errorf("pubsub lookup failed with: %s", err)
	}

	var picks []iface.Serviceable
	for _, serviceable := range serviceables {
		serv, ok := serviceable.(iface.Serviceable)
		if !ok {
			return nil, errors.New("converting serviceable to pubsub serviceable failed")
		}

		picks = append(picks, serv)
	}

	return picks, nil
}

func (s *Service) CheckTns(_matcher commonIface.MatchDefinition) ([]commonIface.Serviceable, error) {
	matcher := _matcher.(*common.MatchDefinition)

	messagingContext, err := s.GetMessagingsMap(matcher)
	if err != nil {
		return nil, err
	} else if !messagingContext.HasAny {
		return nil, fmt.Errorf("no valid messaging configured matches channel %s", matcher.Channel)
	}

	var available = make([]commonIface.Serviceable, 0)
	// get available websocket serviceables
	if messagingContext.WebSocket.Len() > 0 {
		serv, err := websocket.New(s, messagingContext.WebSocket, matcher)
		if err != nil {
			return nil, fmt.Errorf("creating websocket serviceable with `%v` failed with: %w", matcher, err)
		}

		available = append(available, serv)
	}

	if messagingContext.Function.Len() == 0 || matcher.WebSocket {
		if len(available) == 0 {
			return nil, fmt.Errorf("no pub-sub matches found for given matcher %v", matcher)
		}
		return available, nil
	}

	functions, err := s.Tns().Function().All(matcher.Project, matcher.Application, s.Branch()).List()
	if err != nil {
		s.Logger().Error(moody.Object{"message": fmt.Sprintf("Fetching functions list interface failed with: %s", err)})
		return nil, err
	}

	for _, objectPathIface := range functions {
		matches := messagingContext.Function.Matches(objectPathIface.Channel)
		if len(matches) == 0 {
			continue
		}

		var serv commonIface.Serviceable
		serv, err = function.New(s, messagingContext.Function, *objectPathIface, matcher)
		if err != nil {
			s.Logger().Std().Error(fmt.Sprintf("getting Serviceable function failed with: %s", err))
			continue
		}

		available = append(available, serv)
	}

	picks := make([]commonIface.Serviceable, 0)
	for _, serviceable := range available {
		if serviceable.Match(matcher) == matcherSpec.HighMatch {
			picks = append(picks, serviceable)
		}
	}

	if len(picks) == 0 {
		return nil, fmt.Errorf("no pubsub matches found for given matcher %v", matcher)
	}

	return picks, nil
}
