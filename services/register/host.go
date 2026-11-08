package register

import (
	"context"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/service"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/services/cm/settings"
)

// retentionPoll is how often the register's database rereads the region's retention.
const retentionPoll = time.Minute

// Start runs the register on an instance: its rows in a replicated database on the
// peer, which follows the retention the region's settings give.
func (s *server) Start(ctx context.Context, h service.Host) error {
	region := settings.On(h)
	s.region = region
	// The watches hear every change the database commits, merged from another
	// register node as much as written here, so a reader is told either way.
	s.watches = newWatches(defaultWatchLimits)
	db, err := pkgkvdb.Open(h.Peer(), Descriptor.Name,
		pkgkvdb.WithLogger(logging.Logger("tau.register")),
		pkgkvdb.WithObserver(s.watches),
		pkgkvdb.WithRetentionFrom(region.Retention, retentionPoll))
	if err != nil {
		return err
	}
	s.db = db
	store, err := NewKVStore(db)
	if err != nil {
		return err
	}
	if err := s.assemble(h.Peer().Host(), withStore(store)); err != nil {
		return err
	}
	if err := s.serve(ctx); err != nil {
		return err
	}
	h.Serve(Protocol, s)
	return nil
}
