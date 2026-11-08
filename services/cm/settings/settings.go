// Package settings is the region's settings as cm answers them and a service reads
// them. It is cm's, in a package of its own so the services cm itself reads, such as
// the register, can read them too.
package settings

import (
	"context"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	cmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2/cmv2connect"
)

// The region's settings are the numbers every node computes with alike: two nodes
// holding different replica targets disagree forever about who holds what, so no
// service picks its own. cm answers them. Until a cloud can set them they are
// Defaults; once it can, a change reaches every reader with no restart,
// because a reader keeps an answer only until it is Recheck old. Each
// reader caps what it is given by the nodes discovery finds.

// Recheck is how long a reader keeps the settings before asking cm again.
const Recheck = 30 * time.Second

// Protocol is cm's, which answers the settings beside its certificates.
var Protocol protocol.ID = rpc.Protocol(cmv2connect.CertificateServiceName)

// Region is the region's settings.
type Region struct {
	// Replication is how many nodes hold each database.
	Replication int

	// StorageReplicas is R and StorageBarrier K: how many nodes hold each stored
	// object and index shard, and how many confirm a write.
	StorageReplicas int
	StorageBarrier  int

	// Retention is T: how long a removal is remembered. Zero is the database's own
	// default.
	Retention time.Duration
}

// Defaults is what cm answers while nothing has set the region's. Two copies
// of a database, since one dies with its node; three of an object, the figure the
// storage shard arithmetic is sized for, with two confirming a write, since waiting
// for all three makes one slow node the region's write latency.
func Defaults() Region {
	return Region{Replication: 2, StorageReplicas: 3, StorageBarrier: 2}
}

// Source answers the region's settings: cm, or a test's stand-in for it.
type Source interface {
	Settings(ctx context.Context) (Region, error)
}

// Serve mounts the settings service over src.
func Serve(mux *http.ServeMux, src Source) {
	mux.Handle(cmv2connect.NewSettingsServiceHandler(settingsHandler{src}, codec.Option()))
}

type settingsHandler struct{ src Source }

var _ cmv2connect.SettingsServiceHandler = settingsHandler{}

func (h settingsHandler) Current(ctx context.Context, _ *connect.Request[cmv2.Empty]) (*connect.Response[cmv2.Settings], error) {
	s, err := h.src.Settings(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&cmv2.Settings{
		Replication:      uint32(s.Replication),
		StorageReplicas:  uint32(s.StorageReplicas),
		StorageBarrier:   uint32(s.StorageBarrier),
		RetentionSeconds: int64(s.Retention / time.Second),
	}), nil
}

// settingsAt is cm on another node.
type settingsAt struct {
	conn *rpc.Client
	svc  cmv2connect.SettingsServiceClient
}

func dial(h host.Host, id libp2ppeer.ID) (Source, error) {
	conn, err := rpc.NewClient(h, id, Protocol)
	if err != nil {
		return nil, err
	}
	return settingsAt{conn: conn, svc: cmv2connect.NewSettingsServiceClient(conn.HTTP(), conn.URL(), codec.Option())}, nil
}

func (a settingsAt) Settings(ctx context.Context) (Region, error) {
	res, err := a.svc.Current(ctx, connect.NewRequest(&cmv2.Empty{}))
	if err != nil {
		return Region{}, err
	}
	return Region{
		Replication:     int(res.Msg.GetReplication()),
		StorageReplicas: int(res.Msg.GetStorageReplicas()),
		StorageBarrier:  int(res.Msg.GetStorageBarrier()),
		Retention:       time.Duration(res.Msg.GetRetentionSeconds()) * time.Second,
	}, nil
}

func (a settingsAt) Close() error {
	a.conn.Close()
	return nil
}

// Client is the region's settings as a service reads them: from the cm on
// its node when there is one, else from one discovery names, kept for
// Recheck. A failed ask keeps the last answer; with none it is an error, so
// a reader that never heard from cm cannot compute with numbers it guessed.
type Client struct {
	reach *rpc.Reacher[Source]
	now   func() time.Time

	mu      sync.Mutex
	held    Region
	heard   bool
	askedAt time.Time
}

// On is the region's settings as services on h read them.
func On(h service.Host) *Client {
	return &Client{reach: service.Reach(h, Protocol, dial), now: time.Now}
}

// Settings is the region's settings, kept for Recheck.
func (c *Client) Settings(ctx context.Context) (Region, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.heard && c.now().Sub(c.askedAt) < Recheck {
		return c.held, nil
	}
	src, err := c.reach.Now(ctx)
	if err == nil {
		var s Region
		if s, err = src.Settings(ctx); err == nil {
			c.held, c.heard, c.askedAt = s, true, c.now()
			return s, nil
		}
	}
	if c.heard {
		return c.held, nil
	}
	return Region{}, err
}

// Wait returns once cm has answered, asking again every second: a service does not
// start until it has what it needs.
func (c *Client) Wait(ctx context.Context) (Region, error) {
	for {
		s, err := c.Settings(ctx)
		if err == nil {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return Region{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Retention is T, for a database following the region's.
func (c *Client) Retention(ctx context.Context) (time.Duration, error) {
	s, err := c.Settings(ctx)
	return s.Retention, err
}

// Close releases the stream to cm on another node.
func (c *Client) Close() error { return c.reach.Close() }
