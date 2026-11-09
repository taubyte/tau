package instance

import (
	"github.com/ipfs/go-log/v2"
)

// How one service on this node finds another is the node's discovery (extend.go,
// find.go): itself first, for what it runs, then whoever p2p discovery finds.

var logger = log.Logger("tau.v2.node")

// rebroadcastSeconds is how often the CRDT under a database re-announces its heads.
// Five is what every other kvdb in this repo uses; differing would be a number to
// explain.
const rebroadcastSeconds = 5
