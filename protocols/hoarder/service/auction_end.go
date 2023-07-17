package service

import (
	"errors"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
)

func (srv *Service) auctionEnd(auction *hoarderIface.Auction, msg *pubsub.Message) error {
	// All done finalize the lottery
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
	if winner.Lottery.HoarderId == srv.node.Peer().ID().Pretty() {
		err := srv.storeAuction(auction)
		if err != nil {
			return err
		}
	}

	// Do I need to do this, probably not but just in case
	srv.lotteryPool[auction.Meta.ConfigId+auction.Meta.Match] = nil
	return nil
}
