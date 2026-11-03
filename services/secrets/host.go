package secrets

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	logging "github.com/ipfs/go-log/v2"
	peerCore "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/services/register"
)

// retentionPoll is how often the vault's database rereads the cloud's retention.
const retentionPoll = time.Minute

// Start runs this service on an instance: its records in a replicated database on
// the peer, its shares under the instance's directory, its members whoever the
// directory beside it names, else the connected peers that speak the protocol.
//
// A SHARE IS THE ONE PIECE OF KEY MATERIAL A NODE CANNOT RE-DERIVE, so it lives on
// the machine's disk, and an instance with no directory runs no secrets rather than
// holding shares that die with it.
func (srv *server) Start(ctx context.Context, h service.Host) error {
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	if h.Root() == "" {
		return ErrNoRoot
	}
	fleet := register.ViewsOn(h)
	db, err := open(h.Peer(), func(ctx context.Context) (time.Duration, error) {
		return register.Retention(ctx, fleet)
	})
	if err != nil {
		fleet.Close()
		return err
	}
	err = srv.assemble(h.Peer().Host(), db, append(rooted(h.Root(), cfg),
		withMembers(func() []peerCore.ID {
			if d, ok := service.Get[service.Directory](h); ok {
				return d.Peers(Descriptor.Name)
			}
			return srv.speakers()
		}),
		withPubSub(h.Peer().Messaging()))...)
	if err != nil {
		db.Close()
		fleet.Close()
		return err
	}
	srv.owned = append(srv.owned, closer(db.Close), fleet)
	return srv.serve(ctx)
}

// Init writes a sealed bundle into the vault of a node that is not running: the
// database and share store Start would open, through a service that never serves.
// See init.go's Init for what it writes.
func Init(ctx context.Context, p peer.Node, root string, cfg Config, bundle *secretsv2.InitBundle) error {
	if root == "" {
		return ErrNoRoot
	}
	db, err := open(p, nil)
	if err != nil {
		return err
	}
	defer db.Close()
	srv, err := build(p.Host(), db, rooted(root, cfg)...)
	if err != nil {
		return err
	}
	return errors.Join(srv.Init(ctx, bundle), srv.Close())
}

// open is the vault's database on p, following the cloud's retention when told it.
func open(p peer.Node, retention func(context.Context) (time.Duration, error)) (kvdb.Instance, error) {
	opts := []pkgkvdb.OpenOption{pkgkvdb.WithLogger(logging.Logger("tau.secrets"))}
	if retention != nil {
		opts = append(opts, pkgkvdb.WithRetentionFrom(retention, retentionPoll))
	}
	db, err := pkgkvdb.Open(p, Descriptor.Name, opts...)
	if err != nil {
		return nil, fmt.Errorf("secrets: opening the vault's database: %w", err)
	}
	return db, nil
}

// rooted is what every secrets service on a node is built with, serving or not,
// so an init writes the store the service later reads.
func rooted(root string, cfg Config) []option {
	return []option{withRoot(filepath.Join(root, Descriptor.Name)), withConfig(cfg)}
}

type closer func()

func (c closer) Close() error { c(); return nil }
