package hoarder

import (
	"context"
	"sync"

	"github.com/taubyte/tau/core/kvdb"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	ifaceTns "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	streams "github.com/taubyte/tau/p2p/streams/service"
)

// stashClient re-pushes bytes to co-claimants during fan-out (an internal
// hoarder→hoarder client over the same protocol).

var _ hoarderIface.Service = &Service{}

type Service struct {
	node        peer.Node
	zone        string // cluster/region tag reported in heartbeats + placement
	tnsClient   ifaceTns.Client
	db          kvdb.KVDB
	dbFactory   kvdb.Factory
	stream      streams.CommandService
	ldr         *loader
	stashClient hoarderIface.Client
	kvStream    *streamClient.Client

	// membership (heartbeat controller — see membership.go)
	membersLock sync.RWMutex
	members     map[string]*member
	incarnation int64
	hbSeq       uint64

	// reconcileTrigger serializes placement work: an instance hash to reconcile,
	// or "" to reconcile everything (membership change / backstop). Serial by
	// design — no auction storms.
	reconcileTrigger chan string
	reconcileCancel  context.CancelFunc
	// loopsWG tracks the heartbeat, liveness, and reconcile loops so Close can
	// join them after cancel: a canceled-but-still-running loop must not touch
	// srv.db / srv.ldr / the clients while Close tears them down.
	loopsWG   sync.WaitGroup
	atRestKey []byte // cipher key material; nil when this build stores values as-is (set by cipherInit)
}

func (s *Service) Node() peer.Node {
	return s.node
}
