package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/taubyte/go-interfaces/services/substrate"
)

func (g *Gateway) discover() []substrate.Service {
	// find connected substrates over p2p
}

var timeScoreFactor = int(20 * time.Second) // wipName

func (g *Gateway) match(node substrate.Service, matcher Matcher) (score int) {
	var (
		age    int
		exists bool
	)
	age, exists := node.Has(matcher.MatchDefinition)
	if exists {
		score += 10
		score += timeScoreFactor / age
		score += score / matcher.GeoLoc.ParseDistance()
	}
}

type nodeWithScore struct { // wipName
	score int
	node  substrate.Service
}

func (g *Gateway) Match(matcher Matcher) (substrate.Service, error) {
	var highMatch int = -1
	var match substrate.Service
	ctx, ctxC := context.WithTimeout(g.ctx, g.matchTimeout)
	nodeChan := make(chan nodeWithScore, 1)
	doneChan := make(chan struct{})

	go func() {
		for _, node := range g.discover() {
			select {
			case <-ctx.Done():
				return
			default:
				if score := g.match(node, matcher); score > highMatch {
					highMatch, match = score, node
				}
			}
		}
	}()

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-doneChan:
			break loop
		case nodeScore := <-nodeChan:
			if nodeScore.score > highMatch {
				highMatch, match = nodeScore.score, nodeScore.node
			}
		}
	}

	ctxC()

	if match == nil {
		return nil, errors.New("no substrate match found")
	}

	return match, nil

}
