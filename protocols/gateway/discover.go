package gateway

import (
	"net/http"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/p2p/streams/command/response"
)

func (g *Gateway) discover(r *http.Request) (map[peer.ID]response.Response, map[peer.ID]error, error) {
	body := make(map[string]interface{}, 3)
	body["host"], body["path"], body["method"] = r.Host, r.URL.Path, r.Method

	// P2P needs a time out, can update SendToPeerTimeout, but would rather not update global var
	return g.p2pClient.MultiSend("has", body, g.substrateCount/SubstrateThresholdRatio)
}

// var timeScoreFactor = int(20 * time.Second) // wipName

// func (g *Gateway) match(matcher Matcher) (project, resource string, err error) {
// 	score := matcherSpec.DefaultMatch
// 	for {
// 		// loop through find match
// 		if score == matcherSpec.HighMatch {
// 			break
// 		}
// 	}
// 	return
// }

// type nodeWithScore struct { // wipName
// 	score int
// 	node  substrate.Service
// }

// func (g *Gateway) Match(matcher Matcher) (substrate.Service, error) {
// 	var highMatch int = -1
// 	var match substrate.Service
// 	ctx, ctxC := context.WithTimeout(g.ctx, g.matchTimeout)
// 	nodeChan := make(chan nodeWithScore, 1)
// 	doneChan := make(chan struct{})

// 	go func() {
// 		for _, node := range g.discover() {
// 			select {
// 			case <-ctx.Done():
// 				return
// 			default:
// 				if score := g.match(node, matcher); score > highMatch {
// 					highMatch, match = score, node
// 				}
// 			}
// 		}
// 	}()

// 	var done bool
// 	for !done {
// 		select {
// 		case <-ctx.Done():
// 			done = true
// 			break
// 		case <-doneChan:
// 			done = true
// 			break
// 		case nodeScore := <-nodeChan:
// 			if nodeScore.score > highMatch {
// 				highMatch, match = nodeScore.score, nodeScore.node
// 			}
// 		}
// 	}

// 	ctxC()

// 	if match == nil {
// 		return nil, errors.New("no substrate match found")
// 	}

// 	return match, nil

// }
