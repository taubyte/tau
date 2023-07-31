package hoarder

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-datastore"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	hoarderSpecs "github.com/taubyte/go-specs/hoarder"
)

func (srv *Service) validateMsg(auction *hoarderIface.Auction, msg *pubsub.Message) bool {
	// If we get a message from ourselves and its not a timeout/end/failed we ignore
	if msg.ReceivedFrom == srv.node.Peer().ID() && auction.Type != hoarderIface.AuctionEnd {
		return false
	}

	// If we get a message from other people and its end/timeout/ignore we ignore it
	if msg.ReceivedFrom != srv.node.Peer().ID() && auction.Type == hoarderIface.AuctionEnd {
		return false
	}

	return true
}

func (srv *Service) saveAction(auction *hoarderIface.Auction) {
	// Save action and then register it to the lottery pool
	srv.regLock.Lock()
	srv.auctions[auction.Meta.ConfigId+auction.Meta.Match] = auction
	srv.regLock.Unlock()

	newLottery := make([]*hoarderIface.Auction, 0)
	newLottery = append(newLottery, auction)
	srv.regLock.Lock()
	srv.lotteryPool[auction.Meta.ConfigId+auction.Meta.Match] = newLottery
	srv.regLock.Unlock()
}

func (srv *Service) checkValidAction(match string, action hoarderIface.AuctionType, hoarderID string) bool {
	// Check if we have an action history of the match being reported
	srv.regLock.Lock()
	defer srv.regLock.Unlock()

	if _, ok := srv.auctionHistory[match]; !ok {
		// If no reports are found create a new one for that match
		newActionRecord := make(map[string][]hoarderIface.AuctionType)
		srv.auctionHistory[match] = newActionRecord
	}

	actionList, ok := srv.auctionHistory[match][hoarderID]
	if !ok {
		// If no history of action from a specific hoarder record it
		newRecord := make([]hoarderIface.AuctionType, 0)
		newRecord = append(newRecord, action)
		srv.auctionHistory[match][hoarderID] = newRecord
		return false
	}

	// If we do we go through the list and check if they already reported said action
	for _, _action := range actionList {
		if action == _action {
			return true
		}
	}

	// If its not found we register the action then
	actionList = append(actionList, action)
	srv.auctionHistory[match][hoarderID] = actionList
	return false
}

func (srv *Service) publishAction(ctx context.Context, action *hoarderIface.Auction, actionType hoarderIface.AuctionType) error {
	action.Type = actionType
	actionBytes, err := cbor.Marshal(action)
	if err != nil {
		return fmt.Errorf("failed marshalling action with %v", err)
	}

	if err = srv.node.PubSubPublish(ctx, hoarderSpecs.PubSubIdent, actionBytes); err != nil {
		return fmt.Errorf("publish to `%s` failed with: %s", hoarderSpecs.PubSubIdent, err)
	}

	return nil
}

func (srv *Service) storeAuction(ctx context.Context, auction *hoarderIface.Auction) error {
	switch auction.MetaType {
	case hoarderIface.Database:
		config, err := srv.tnsClient.Database().All(auction.Meta.ProjectId, auction.Meta.ApplicationId, auction.Meta.Branch).GetById(auction.Meta.ConfigId)
		if err != nil {
			return fmt.Errorf("getting database with id `%s` failed with: %s", auction.Meta.ConfigId, err)
		}

		if err = checkMatch(config.Regex, auction.Meta.Match, config.Match, config.Name); err != nil {
			return err
		}

		configBytes, err := cbor.Marshal(config)
		if err != nil {
			return err
		}

		if !strings.HasPrefix(auction.Meta.Match, "/") {
			auction.Meta.Match = "/" + auction.Meta.Match
		}

		if err = srv.putIntoDb(ctx, datastore.NewKey(fmt.Sprintf("/hoarder/databases/%s%s", auction.Meta.ConfigId, auction.Meta.Match)), configBytes); err != nil {
			return err
		}

		return nil

	case hoarderIface.Storage:
		config, err := srv.tnsClient.Storage().All(auction.Meta.ProjectId, auction.Meta.ApplicationId, auction.Meta.Branch).GetById(auction.Meta.ConfigId)
		if err != nil {
			return err
		}

		if err = checkMatch(config.Regex, auction.Meta.Match, config.Match, config.Name); err != nil {
			return err
		}

		configBytes, err := cbor.Marshal(config)
		if err != nil {
			return err
		}

		if !strings.HasPrefix(auction.Meta.Match, "/") {
			auction.Meta.Match = "/" + auction.Meta.Match
		}

		if err = srv.putIntoDb(ctx, datastore.NewKey(fmt.Sprintf("/hoarder/storages/%s%s", auction.Meta.ConfigId, auction.Meta.Match)), configBytes); err != nil {
			return err
		}

		return nil

	}

	return errors.New("auction item was neither a storage or database")
}

func (srv *Service) putIntoDb(ctx context.Context, key datastore.Key, data []byte) error {
	srv.regLock.Lock()
	defer srv.regLock.Unlock()
	if err := srv.db.Put(ctx, key.String(), data); err != nil {
		return err
	}
	return nil
}
