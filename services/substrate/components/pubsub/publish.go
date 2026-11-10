package pubsub

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

func (s *Service) Publish(ctx context.Context, projectId, appId, resource, channel string, data []byte) error {
	matcher := &common.MatchDefinition{
		Channel:     channel,
		Project:     projectId,
		Application: appId,
	}

	// Only that the channel is declared matters: having no function or
	// websocket listening on it yet is not a publish failure.
	messaging, _, _, err := s.getMessagingsMap(matcher)
	if err != nil {
		return fmt.Errorf("getting messaging for channel %q failed with: %w", channel, err)
	}
	if !messaging.HasAny {
		return fmt.Errorf("no messaging matches channel %q", channel)
	}

	matcher.Channel = strings.TrimPrefix(matcher.Channel, "/")

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
