package pubsub

import (
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

func (s *Service) WebSocketURL(projectId, appId, channel string) (string, error) {
	matcher := &common.MatchDefinition{
		Channel:     channel,
		Project:     projectId,
		Application: appId,
		WebSocket:   true,
	}

	if matcher.Channel[0] == '/' {
		matcher.Channel = matcher.Channel[1:]
	}

	// Ignoring picks because if there are no picks there is an error returned
	_, err := s.Lookup(matcher)
	if err != nil {
		common.Logger.Error("Socket generation failed with err:", err.Error())
		return "", err
	}

	return matcher.GenerateSocketURL(), nil
}
