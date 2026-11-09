package patrick

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/kvdb"
)

type Client interface {
	Dequeue() (*Job, error)
	IsAssigned(jid string) (bool, error)
	Refresh(jid string) (bool, error)
	Done(jid string, cid_log map[string]string, assetCid map[string]string) error
	// Failed reports a failed run. A non-empty reason means another run would fail the same way, so the job is not retried.
	Failed(jid string, cid_log map[string]string, assetCid map[string]string, reason string) error
	List() ([]string, error)
	Get(jid string) (*Job, error)
	Timeout(jid string) error
	Cancel(jid string, cid_log map[string]string) (interface{}, error)
	DatabaseStats() (kvdb.Stats, error)
	Peers(...peerCore.ID) Client
	Close()
}
