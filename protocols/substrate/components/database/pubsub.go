package database

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/go-interfaces/services/hoarder"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	hoarderSpecs "github.com/taubyte/go-specs/hoarder"
)

func (s *Service) pubsubDatabase(context iface.Context, branch string) error {
	auction := &hoarder.Auction{
		Type:     hoarder.AuctionNew,
		MetaType: hoarder.Database,
		Meta: hoarder.MetaData{
			ConfigId:      context.Config.Id,
			ApplicationId: context.ApplicationId,
			ProjectId:     context.ProjectId,
			Match:         context.Matcher,
			Branch:        s.Branch(),
		},
	}

	dataBytes, err := cbor.Marshal(auction)
	if err != nil {
		return fmt.Errorf("marshalling auction failed with %w", err)
	}

	topic, err := s.Node().Messaging().Join(hoarderSpecs.PubSubIdent)
	if err != nil {
		return fmt.Errorf("getting topic `%s` failed with: %w", hoarderSpecs.PubSubIdent, err)
	}

	if err = topic.Publish(s.Context(), dataBytes); err != nil {
		return fmt.Errorf("publishing database `%s` failed with %w", context.Matcher, err)
	}

	return nil
}
