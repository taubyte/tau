package pubsub

import (
	"context"
	"errors"

	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

func (s *Service) Publish(ctx context.Context, projectId, appId, channel string, data []byte) error {
	matcher := &common.MatchDefinition{
		Channel:     channel,
		Project:     projectId,
		Application: appId,
	}

	picks, err := s.Lookup(matcher)
	if err != nil {
		common.Logger.Error("lookup failed with: %s", err.Error())
		return err
	}
	if len(picks) == 0 {
		common.Logger.Error("lookup returned no picks")
		return errors.New("asset not found")
	}

	if matcher.Channel[0] == '/' {
		matcher.Channel = matcher.Channel[1:]
	}

	// TODO smartops for the messaging channel
	return s.Node().PubSubPublish(ctx, matcher.Path(), data)
}
