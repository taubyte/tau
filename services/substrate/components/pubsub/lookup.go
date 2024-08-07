package pubsub

import (
	"errors"
	"fmt"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
	"github.com/taubyte/tau/services/substrate/components/pubsub/function"
	"github.com/taubyte/tau/services/substrate/components/pubsub/websocket"
	"github.com/taubyte/tau/services/substrate/runtime/lookup"
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

	messagingContext, commit, branch, err := s.getMessagingsMap(matcher)
	if err != nil {
		return nil, err
	} else if !messagingContext.HasAny {
		return nil, fmt.Errorf("no valid messaging configured matches channel %s", matcher.Channel)
	}

	var available = make([]commonIface.Serviceable, 0)
	// get available websocket serviceables
	if messagingContext.WebSocket.Len() > 0 {
		serv, err := websocket.New(s, messagingContext.WebSocket, commit, branch, matcher)
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

	functions, commit, branch, err := s.Tns().Function().All(matcher.Project, matcher.Application, branch).List()
	if err != nil {
		common.Logger.Error("fetching functions list interface failed with:", err.Error())
		return nil, err
	}

	for _, objectPathIface := range functions {
		matches := messagingContext.Function.Matches(objectPathIface.Channel)
		if len(matches) == 0 {
			continue
		}

		var serv commonIface.Serviceable
		serv, err = function.New(s, messagingContext.Function, *objectPathIface, commit, branch, matcher)
		if err != nil {
			common.Logger.Error("getting Serviceable function failed with:", err.Error())
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
