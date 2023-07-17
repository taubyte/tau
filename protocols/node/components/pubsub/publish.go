package pubsub

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
)

func (s *Service) Publish(ctx context.Context, projectId, appId, channel string, data []byte) error {
	matcher := &common.MatchDefinition{
		Channel:     channel,
		Project:     projectId,
		Application: appId,
	}

	picks, err := s.Lookup(matcher)
	if err != nil {
		s.Logger().Std().Error(fmt.Sprintf("lookup failed with err: %s", err.Error()))
		return err
	}
	if len(picks) == 0 {
		s.Logger().Std().Error("pick==nil failed with err")
		return errors.New("asset not found")
	}

	if matcher.Channel[0] == '/' {
		matcher.Channel = matcher.Channel[1:]
	}

	// TODO smartops for the messaging channel
	return s.Node().PubSubPublish(ctx, matcher.Path(), data)
}
