package hoarder

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"
	"unsafe"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
)

func (srv *Service) auctionNew(ctx context.Context, auction *hoarderIface.Auction, msg *pubsub.Message) error {
	srv.startAuction(ctx, auction)

	// Check if we have that actionId stored with the action
	if found := srv.checkValidAction(auction.Meta.Match, hoarderIface.AuctionNew, msg.ReceivedFrom.String()); !found {
		// Generate Lottery number and publish our intent to join the lottery
		auction.Lottery.HoarderId = srv.node.ID().String()

		arr := make([]byte, 8)
		if _, err := rand.Read(arr); err != nil {
			return fmt.Errorf("auctionNew rand read failed with: %s", err)
		}

		num := *(*uint64)(unsafe.Pointer(&arr[0]))
		auction.Lottery.Number = num

		if err := srv.publishAction(ctx, auction, hoarderIface.AuctionIntent); err != nil {
			return err
		}
	}

	// Store the new action and register our entry inside the auction pool
	srv.saveAction(auction)
	return nil
}

func (srv *Service) startAuction(ctx context.Context, action *hoarderIface.Auction) {
	// Start a countdown for the new action
	go func() {
		select {
		case <-ctx.Done():
			return

		case <-time.After(maxWaitTime):
			if err := srv.publishAction(ctx, action, hoarderIface.AuctionEnd); err != nil {
				logger.Error("action publish failed with:", err.Error())
			}
		}
	}()
}

func (srv *Service) auctionEnd(ctx context.Context, auction *hoarderIface.Auction, msg *pubsub.Message) error {
	var winner *hoarderIface.Auction
	var currentBiggest uint64
	for _, lottery := range srv.lotteryPool[auction.Meta.ConfigId+auction.Meta.Match] {
		if lottery.Lottery.Number > currentBiggest {
			winner = lottery
			currentBiggest = lottery.Lottery.Number
		}
	}

	if winner == nil {
		return errors.New("no winner was selected")
	}

	// Self evaluate to check if we won or not
	if winner.Lottery.HoarderId == srv.node.Peer().ID().String() {
		err := srv.storeAuction(ctx, auction)
		if err != nil {
			return err
		}
	}

	// Do I need to do this, probably not but just in case
	srv.lotteryPool[auction.Meta.ConfigId+auction.Meta.Match] = nil
	return nil
}

func (srv *Service) auctionIntent(auction *hoarderIface.Auction, msg *pubsub.Message) error {
	// If we see that the node already reported intent to stash on action Id we ignore it
	if found := srv.checkValidAction(auction.Meta.Match, hoarderIface.AuctionIntent, msg.ReceivedFrom.String()); found {
		return fmt.Errorf("%s already reported an intent", msg.ReceivedFrom.String())
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
