package service

import (
	"crypto/rand"
	"fmt"
	"time"
	"unsafe"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
)

const maxWaitTime = 15 * time.Second

func (srv *Service) auctionNew(auction *hoarderIface.Auction, msg *pubsub.Message) error {
	srv.startAuction(auction)

	// Check if we have that actionId stored with the action
	if found := srv.checkValidAction(auction.Meta.Match, hoarderIface.AuctionNew, msg.ReceivedFrom.Pretty()); !found {
		// Generate Lottery number and publish our intent to join the lottery
		auction.Lottery.HoarderId = srv.node.ID().Pretty()

		arr := make([]byte, 8)
		if _, err := rand.Read(arr); err != nil {
			return fmt.Errorf("auctionNew rand read failed with: %s", err)
		}

		num := *(*uint64)(unsafe.Pointer(&arr[0]))
		auction.Lottery.Number = num

		if err := srv.publishAction(srv.ctx, auction, hoarderIface.AuctionIntent); err != nil {
			return err
		}
	}

	// Store the new action and register our entry inside the auction pool
	srv.saveAction(auction)
	return nil
}

func (srv *Service) startAuction(action *hoarderIface.Auction) {
	// Start a countdown for the new action
	go func() {
		select {
		case <-srv.ctx.Done():
			return

		case <-time.After(maxWaitTime):
			if err := srv.publishAction(srv.ctx, action, hoarderIface.AuctionEnd); err != nil {
				logger.Errorf("action publish failed with: %s", err)
			}
		}
	}()
}
