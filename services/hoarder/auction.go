package hoarder

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
	"unsafe"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
)

const debugLogPath = "/home/samy/Documents/taubyte/github/tau/.cursor/debug.log"

func debugLog(location, message string, data map[string]interface{}) {
	logEntry := map[string]interface{}{
		"timestamp": time.Now().UnixMilli(),
		"location":  location,
		"message":   message,
		"data":      data,
		"sessionId": "hoarder-auction",
		"runId":     "run1",
	}
	jsonBytes, err := json.Marshal(logEntry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(debugLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(jsonBytes)
	f.WriteString("\n")
}

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

		// #region agent log
		debugLog("auction.go:auctionNew", "Hoarder joining lottery", map[string]interface{}{
			"hoarderId":     auction.Lottery.HoarderId,
			"lotteryNumber": auction.Lottery.Number,
			"configId":      auction.Meta.ConfigId,
			"match":         auction.Meta.Match,
			"metaType":      auction.MetaType,
		})
		// #endregion

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
	poolKey := auction.Meta.ConfigId + auction.Meta.Match
	pool := srv.lotteryPool[poolKey]

	// #region agent log
	debugLog("auction.go:auctionEnd:entry", "Auction ending, evaluating lottery pool", map[string]interface{}{
		"configId":    auction.Meta.ConfigId,
		"match":       auction.Meta.Match,
		"poolKey":     poolKey,
		"poolSize":    len(pool),
		"thisHoarder": srv.node.Peer().ID().String(),
	})
	logger.Errorf("*** AUCTION END: configId=%s match=%s poolSize=%d thisHoarder=%s ***", auction.Meta.ConfigId, auction.Meta.Match, len(pool), srv.node.Peer().ID().String())
	// #endregion

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

	// #region agent log
	debugLog("auction.go:auctionEnd:participants", "All lottery participants", map[string]interface{}{
		"configId":     auction.Meta.ConfigId,
		"match":        auction.Meta.Match,
		"participants": allParticipants,
	})
	logger.Errorf("*** LOTTERY PARTICIPANTS: configId=%s match=%s participants=%d ***", auction.Meta.ConfigId, auction.Meta.Match, len(allParticipants))
	for i, p := range allParticipants {
		logger.Errorf("  *** Participant %d: hoarderId=%s lotteryNumber=%d ***", i+1, p["hoarderId"], p["lotteryNumber"])
	}
	// #endregion

	if winner == nil {
		// #region agent log
		debugLog("auction.go:auctionEnd:no-winner", "No winner selected", map[string]interface{}{
			"configId": auction.Meta.ConfigId,
			"match":    auction.Meta.Match,
		})
		// #endregion
		return errors.New("no winner was selected")
	}

	// #region agent log
	debugLog("auction.go:auctionEnd:winner", "Winner determined", map[string]interface{}{
		"configId":     auction.Meta.ConfigId,
		"match":        auction.Meta.Match,
		"winnerId":     winner.Lottery.HoarderId,
		"winnerNumber": winner.Lottery.Number,
		"thisHoarder":  srv.node.Peer().ID().String(),
		"isWinner":     winner.Lottery.HoarderId == srv.node.Peer().ID().String(),
	})
	logger.Infof("WINNER: configId=%s match=%s winnerId=%s winnerNumber=%d thisHoarder=%s isWinner=%v",
		auction.Meta.ConfigId, auction.Meta.Match, winner.Lottery.HoarderId, winner.Lottery.Number,
		srv.node.Peer().ID().String(), winner.Lottery.HoarderId == srv.node.Peer().ID().String())
	// #endregion

	// Self evaluate to check if we won or not
	if winner.Lottery.HoarderId == srv.node.Peer().ID().String() {
		// #region agent log
		debugLog("auction.go:auctionEnd:storing", "This hoarder won, storing config", map[string]interface{}{
			"configId":  auction.Meta.ConfigId,
			"match":     auction.Meta.Match,
			"hoarderId": winner.Lottery.HoarderId,
		})
		storeMsg := fmt.Sprintf("*** STORING CONFIG: configId=%s match=%s hoarderId=%s ***", auction.Meta.ConfigId, auction.Meta.Match, winner.Lottery.HoarderId)
		logger.Errorf(storeMsg)
		fmt.Printf("[HOARDER-AUCTION] %s\n", storeMsg)
		// #endregion
		err := srv.storeAuction(ctx, auction)
		if err != nil {
			// #region agent log
			debugLog("auction.go:auctionEnd:store-error", "Failed to store config", map[string]interface{}{
				"configId":  auction.Meta.ConfigId,
				"match":     auction.Meta.Match,
				"hoarderId": winner.Lottery.HoarderId,
				"error":     err.Error(),
			})
			logger.Errorf("STORE FAILED: configId=%s match=%s hoarderId=%s error=%v", auction.Meta.ConfigId, auction.Meta.Match, winner.Lottery.HoarderId, err)
			// #endregion
			return err
		}
		// #region agent log
		debugLog("auction.go:auctionEnd:stored", "Config stored successfully", map[string]interface{}{
			"configId":  auction.Meta.ConfigId,
			"match":     auction.Meta.Match,
			"hoarderId": winner.Lottery.HoarderId,
		})
		logger.Infof("CONFIG STORED: configId=%s match=%s hoarderId=%s", auction.Meta.ConfigId, auction.Meta.Match, winner.Lottery.HoarderId)
		// #endregion
	} else {
		// #region agent log
		debugLog("auction.go:auctionEnd:not-winner", "This hoarder did not win", map[string]interface{}{
			"configId":    auction.Meta.ConfigId,
			"match":       auction.Meta.Match,
			"thisHoarder": srv.node.Peer().ID().String(),
			"winnerId":    winner.Lottery.HoarderId,
		})
		logger.Errorf("*** NOT WINNER: configId=%s match=%s thisHoarder=%s winnerId=%s ***",
			auction.Meta.ConfigId, auction.Meta.Match, srv.node.Peer().ID().String(), winner.Lottery.HoarderId)
		// #endregion
	}

	// Do I need to do this, probably not but just in case
	srv.lotteryPool[poolKey] = nil
	return nil
}

func (srv *Service) auctionIntent(auction *hoarderIface.Auction, msg *pubsub.Message) error {
	// If we see that the node already reported intent to stash on action Id we ignore it
	if found := srv.checkValidAction(auction.Meta.Match, hoarderIface.AuctionIntent, msg.ReceivedFrom.String()); found {
		// #region agent log
		debugLog("auction.go:auctionIntent:duplicate", "Duplicate intent ignored", map[string]interface{}{
			"hoarderId": msg.ReceivedFrom.String(),
			"configId":  auction.Meta.ConfigId,
			"match":     auction.Meta.Match,
		})
		// #endregion
		return fmt.Errorf("%s already reported an intent", msg.ReceivedFrom.String())
	}

	// Generate lottery pool
	srv.regLock.Lock()
	defer srv.regLock.Unlock()
	poolKey := auction.Meta.ConfigId + auction.Meta.Match
	pool, ok := srv.lotteryPool[poolKey]
	if !ok {
		return fmt.Errorf("did not find lottery pool for %s", poolKey)
	}

	// #region agent log
	debugLog("auction.go:auctionIntent:received", "Received intent from hoarder", map[string]interface{}{
		"hoarderId":      auction.Lottery.HoarderId,
		"lotteryNumber":  auction.Lottery.Number,
		"configId":       auction.Meta.ConfigId,
		"match":          auction.Meta.Match,
		"poolSizeBefore": len(pool),
		"fromPeer":       msg.ReceivedFrom.String(),
	})
	// #endregion

	pool = append(pool, auction)
	srv.lotteryPool[poolKey] = pool

	// #region agent log
	debugLog("auction.go:auctionIntent:added", "Intent added to pool", map[string]interface{}{
		"hoarderId":     auction.Lottery.HoarderId,
		"configId":      auction.Meta.ConfigId,
		"match":         auction.Meta.Match,
		"poolSizeAfter": len(pool),
	})
	// #endregion

	return nil
}
