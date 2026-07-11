package hoarder

import (
	"context"
	"io"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/kvdb"
)

type Client interface {
	Rare() ([]string, error)
	// Stash pushes the CID's bytes to a hoarder, which verifies, pins, claims,
	// and fans out to co-claimants. data is streamed after a small header.
	Stash(cid string, data io.Reader, opts ...StashOption) error
	List() ([]string, error)
	// ReplicasOf resolves the live holder peers of a database/storage instance.
	ReplicasOf(kind ResourceKind, project, application, match string) ([]peerCore.ID, error)
	// KVDB returns a remote-backed kvdb.KVDB for a database/storage instance —
	// operations are p2p calls to the hoarders holding it.
	KVDB(kind ResourceKind, project, application, match, branch string) (kvdb.KVDB, error)
	// Metas resolves instance hashes to their placement identity records; hashes
	// with no record are omitted. Lets a node that knows only a data-path hash
	// recover the instance's identity.
	Metas(hashes ...string) ([]InstanceInfo, error)
	// StashStatus reports the live stash claim count per CID (0 = unknown) and
	// the current fleet-clamped stash replica target — the check a byte holder
	// runs before dropping its local copy.
	StashStatus(cids ...string) (claims map[string]int, target int, err error)
	Peers(...peerCore.ID) Client
	Close()
}

// InstanceInfo is a placement identity record as returned by Metas.
type InstanceInfo struct {
	Hash string
	Kind ResourceKind
	Meta MetaData
}

// NxKVDB is a KVDB handle that also supports conditional writes. The remote
// hoarder-backed handle implements it; PutNx returns existed=true when the key
// was already present on the serving replica and nothing was written.
type NxKVDB interface {
	kvdb.KVDB
	PutNx(ctx context.Context, key string, value []byte) (existed bool, err error)
}

// StashConfig carries push options. Target is the desired replica count; Owner
// is the storage instance hash the blocks belong to; Fanout is whether the
// receiving hoarder re-pushes to co-claimants (false for hoarder→hoarder
// re-replication to avoid a storm).
type StashConfig struct {
	Target int
	Owner  string
	Fanout bool
}

type StashOption func(*StashConfig)

func WithTarget(n int) StashOption {
	return func(c *StashConfig) { c.Target = n }
}

func WithOwner(hash string) StashOption {
	return func(c *StashConfig) { c.Owner = hash }
}

// WithoutFanout marks a hoarder→hoarder re-push so the receiver does not fan
// out again.
func WithoutFanout() StashOption {
	return func(c *StashConfig) { c.Fanout = false }
}
