package service

import (
	"context"
	"time"

	iface "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	http "github.com/taubyte/tau/pkg/http"

	auth "github.com/taubyte/tau/core/services/auth"

	monkey "github.com/taubyte/tau/core/services/monkey"
	tns "github.com/taubyte/tau/core/services/tns"

	"github.com/taubyte/tau/core/kvdb"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/pkg/raft"
)

var _ iface.Service = &PatrickService{}

type PatrickService struct {
	ctx          context.Context
	cancel       context.CancelFunc
	monkeyClient monkey.Client
	node         peer.Node
	http         http.Service
	stream       streams.CommandService
	authClient   auth.Client
	tnsClient    tns.Client
	db           kvdb.KVDB
	dbFactory    kvdb.Factory
	devMode      bool
	// reAnnounceJobTime is captured per-service at construction (from the
	// DefaultReAnnounceJobTime default, or 5s in dev mode). It replaces a former
	// runtime mutation of the package global, which raced across concurrent
	// service instances.
	reAnnounceJobTime time.Duration

	cluster        string
	raftCluster    raft.Cluster
	jobQueue       raft.Queue
	outboundClient *streamClient.Client

	hostUrl string
}

// Assignment tracks which Monkey is working on a job.
type Assignment struct {
	MonkeyPID string `cbor:"1,keyasint"`
	Timestamp int64  `cbor:"2,keyasint"`
}
