package service

import (
	"fmt"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
)

func (srv *Service) auctionIntent(auction *hoarderIface.Auction, msg *pubsub.Message) error {
	// If we see that the node already reported intent to stash on action Id we ignore it
	if found := srv.checkValidAction(auction.Meta.Match, hoarderIface.AuctionIntent, msg.ReceivedFrom.Pretty()); found {
		return fmt.Errorf("%s already reported an intent", msg.ReceivedFrom.Pretty())
	}

	// Generate lottery pool
	srv.regLock.Lock()
	defer srv.regLock.Unlock()
	pool, ok := srv.lotteryPool[auction.Meta.ConfigId+auction.Meta.Match]
	if !ok {
		return fmt.Errorf("did not find lottery pool for %s", auction.Meta.ConfigId+auction.Meta.Match)
	}

	pool = append(pool, auction)
	srv.lotteryPool[auction.Meta.ConfigId+auction.Meta.Match] = pool
	return nil
}
