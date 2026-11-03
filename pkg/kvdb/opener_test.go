package kvdb

import (
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/p2p/peer"
)

// opener opens databases on one peer for these tests.
type opener struct{ peer.Node }

func (o opener) New(logger logging.StandardLogger, path string, rebroadcastSec int) (*kvDatabase, error) {
	return open(o.Node, path, WithLogger(logger), WithRebroadcast(time.Duration(rebroadcastSec)*time.Second))
}
