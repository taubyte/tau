package hoarder

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-datastore"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	databaseSpec "github.com/taubyte/go-specs/database"
	hoarderSpecs "github.com/taubyte/go-specs/hoarder"
	storageSpec "github.com/taubyte/go-specs/storage"
)

func handleRegex(pattern, match string) error {
	matched, err := regexp.Match(pattern, []byte(match))
	if err != nil {
		return fmt.Errorf("parsing regex pattern `%s` failed with: %w", pattern, err)
	}

	if !matched {
		return fmt.Errorf("`%s` does not match regex pattern `%s`", match, pattern)
	}

	return nil
}

func checkMatch(regex bool, match, toMatch, name string) error {
	if regex {
		return handleRegex(toMatch, match)
	}

	if match != toMatch {
		return fmt.Errorf("no match %s != %s", match, toMatch)
	}
	return nil
}

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
		return fmt.Errorf("failed marshalling action with %w", err)
	}

	if err = srv.node.PubSubPublish(ctx, hoarderSpecs.PubSubIdent, actionBytes); err != nil {
		return fmt.Errorf("publish to `%s` failed with: %w", hoarderSpecs.PubSubIdent, err)
	}

	return nil
}

func (srv *Service) storeAuction(ctx context.Context, auction *hoarderIface.Auction) error {
	var (
		metaType string
		config   any
		match    string
		name     string
		regex    bool
	)

	switch auction.MetaType {
	case hoarderIface.Database:
		db, err := srv.tnsClient.Database().All(auction.Meta.ProjectId, auction.Meta.ApplicationId, auction.Meta.Branch).GetById(auction.Meta.ConfigId)
		if err != nil {
			return fmt.Errorf("getting database with id `%s` failed with: %w", auction.Meta.ConfigId, err)
		}
		config, match, name, regex, metaType = db, db.Match, db.Name, db.Regex, databaseSpec.PathVariable.String()
	case hoarderIface.Storage:
		stor, err := srv.tnsClient.Storage().All(auction.Meta.ProjectId, auction.Meta.ApplicationId, auction.Meta.Branch).GetById(auction.Meta.ConfigId)
		if err != nil {
			return fmt.Errorf("getting storage with id `%s` failed with: %w", auction.Meta.ConfigId, err)
		}

		config, match, name, regex, metaType = stor, stor.Match, stor.Name, stor.Regex, storageSpec.PathVariable.String()
	default:
		return fmt.Errorf("invalid meta type %d", auction.MetaType)
	}

	if err := checkMatch(regex, auction.Meta.Match, match, name); err != nil {
		return fmt.Errorf("checking auction match failed with: %w", err)
	}

	configBytes, err := cbor.Marshal(config)
	if err != nil {
		return fmt.Errorf("cbor marshal of config failed with: %w", err)
	}

	if !strings.HasPrefix(auction.Meta.Match, "/") {
		auction.Meta.Match = "/" + auction.Meta.Match
	}

	srv.regLock.Lock()
	defer srv.regLock.Unlock()

	key := datastore.NewKey(fmt.Sprintf("/hoarder/%s/%s%s", metaType, auction.Meta.ConfigId, auction.Meta.Match))
	if err := srv.db.Put(ctx, key.String(), configBytes); err != nil {
		return fmt.Errorf("put failed with: %w", err)
	}

	return err
}
