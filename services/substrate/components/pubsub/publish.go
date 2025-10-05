package pubsub

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

func (s *Service) Publish(ctx context.Context, projectId, appId, resource, channel string, data []byte) error {
	matcher := &common.MatchDefinition{
		Channel:     channel,
		Project:     projectId,
		Application: appId,
	}

	picks, err := s.Lookup(matcher)
	if err != nil {
		return fmt.Errorf("lookup failed with: %w", err)
	}
	if len(picks) == 0 {
		return errors.New("lookup returned no picks")
	}

	if matcher.Channel[0] == '/' {
		matcher.Channel = matcher.Channel[1:]
	}

	message, err := common.NewMessage(
		data,
		resource, /* id of function - unique to each function - at least on channel*/
	)
	if err != nil {
		return fmt.Errorf("creating message failed with: %w", err)
	}

	data, err = message.Marshal()
	if err != nil {
		return fmt.Errorf("marshalling message failed with: %w", err)
	}

	// TODO smartops for the messaging channel
	return s.Node().PubSubPublish(ctx, matcher.String(), data)
}
