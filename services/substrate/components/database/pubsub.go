package database

import (
	"fmt"
	"time"

	"context"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/services/hoarder"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

func (s *Service) pubsubDatabase(ctx iface.Context, branch string) error {
	auction := &hoarder.Auction{
		Type:     hoarder.AuctionNew,
		MetaType: hoarder.Database,
		Meta: hoarder.MetaData{
			ConfigId:      ctx.Config.Id,
			ApplicationId: ctx.ApplicationId,
			ProjectId:     ctx.ProjectId,
			Match:         ctx.Matcher,
			Branch:        branch,
		},
	}

	dataBytes, err := cbor.Marshal(auction)
	if err != nil {
		return fmt.Errorf("marshalling auction failed with %w", err)
	}

	pubsubCtx, pubsubCtxC := context.WithTimeout(s.Node().Context(), 10*time.Second)
	defer pubsubCtxC()

	if err := s.Node().PubSubPublish(pubsubCtx, hoarderSpecs.PubSubIdent, dataBytes); err != nil {
		return fmt.Errorf("publishing database `%s` failed with %w", ctx.Matcher, err)
	}

	return nil
}
