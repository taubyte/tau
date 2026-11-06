package register

import (
	"context"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/service"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/register"
)

// retentionPoll is how often the register's database rereads the cloud's retention.
const retentionPoll = time.Minute

// Start runs the register on an instance: its rows in a replicated database on the
// peer, which follows the retention the register's own fleet document gives.
func (s *server) Start(ctx context.Context, h service.Host) error {
	// THE REGISTER IS BUILT OVER THE DATABASE IT FOLLOWS, so the follower reads
	// it once it exists and the default until then.
	var built atomic.Pointer[register.Register]
	// The watches hear every change the database commits, merged from another
	// register node as much as written here, so a reader is told either way.
	s.watches = newWatches(defaultWatchLimits)
	db, err := pkgkvdb.Open(h.Peer(), Descriptor.Name,
		pkgkvdb.WithLogger(logging.Logger("tau.register")),
		pkgkvdb.WithObserver(s.watches),
		pkgkvdb.WithRetentionFrom(func(ctx context.Context) (time.Duration, error) {
			reg := built.Load()
			if reg == nil {
				return 0, nil
			}
			return Retention(ctx, reg)
		}, retentionPoll))
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
	built.Store(s.reg)
	return s.serve(ctx)
}
