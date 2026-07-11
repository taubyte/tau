package hoarder

import (
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
	Peers(...peerCore.ID) Client
	Close()
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
