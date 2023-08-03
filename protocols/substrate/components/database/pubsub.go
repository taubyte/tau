package database

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/go-interfaces/services/hoarder"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	spec "github.com/taubyte/go-specs/common"
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
			Branch:        spec.DefaultBranch,
		},
	}

	dataBytes, err := cbor.Marshal(auction)
	if err != nil {
		return fmt.Errorf("marshalling auction failed with %w", err)
	}

	if err := s.Node().Messaging().Publish(hoarderSpecs.PubSubIdent, dataBytes); err != nil {
		return fmt.Errorf("publishing database `%s` failed with %w", context.Matcher, err)
	}

	return nil
}
