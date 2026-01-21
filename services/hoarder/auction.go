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

	if found := srv.checkValidAction(auction.Meta.Match, hoarderIface.AuctionNew, msg.ReceivedFrom.String()); !found {
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

	srv.saveAction(auction)
	return nil
}

func (srv *Service) startAuction(ctx context.Context, action *hoarderIface.Auction) {
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
	poolKey := auction.Meta.ConfigId + auction.Meta.Match
	pool := srv.lotteryPool[poolKey]

	var winner *hoarderIface.Auction
	var currentBiggest uint64
	allParticipants := make([]map[string]interface{}, 0, len(pool))
	for _, lottery := range pool {
		allParticipants = append(allParticipants, map[string]interface{}{
			"hoarderId":     lottery.Lottery.HoarderId,
			"lotteryNumber": lottery.Lottery.Number,
		})
		if lottery.Lottery.Number > currentBiggest {
			winner = lottery
			currentBiggest = lottery.Lottery.Number
		}
	}

	if winner == nil {
		return errors.New("no winner was selected")
	}

	if winner.Lottery.HoarderId == srv.node.Peer().ID().String() {
		err := srv.storeAuction(ctx, auction)
		if err != nil {
			return err
		}
	}

	srv.lotteryPool[poolKey] = nil
	return nil
}

func (srv *Service) auctionIntent(auction *hoarderIface.Auction, msg *pubsub.Message) error {
	if found := srv.checkValidAction(auction.Meta.Match, hoarderIface.AuctionIntent, msg.ReceivedFrom.String()); found {
		return fmt.Errorf("%s already reported an intent", msg.ReceivedFrom.String())
	}

	srv.regLock.Lock()
	defer srv.regLock.Unlock()
	poolKey := auction.Meta.ConfigId + auction.Meta.Match
	pool, ok := srv.lotteryPool[poolKey]
	if !ok {
		return fmt.Errorf("did not find lottery pool for %s", poolKey)
	}

	pool = append(pool, auction)
	srv.lotteryPool[poolKey] = pool

	return nil
}
