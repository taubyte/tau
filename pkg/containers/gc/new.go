package gc

import (
	"context"
	"time"

	"github.com/ipfs/go-log/v2"
	ci "github.com/taubyte/tau/pkg/containers"
)

var logger = log.Logger("tau.containers.gc")

type config struct {
	interval  time.Duration
	maxAge    time.Duration
	reference string
}

var (
	DefaultInterval = 30 * time.Minute
	DefaultMaxAge   = 24 * time.Hour
)

// Starts a new garbage collector with the specified interval check, and removes containers older than specified age.
func Start(ctx context.Context, options ...Option) error {
	client, err := ci.New()
	if err != nil {
		return err
	}

	cnf := &config{
		interval: DefaultInterval,
		maxAge:   DefaultMaxAge,
	}
	for _, opt := range options {
		if err := opt(cnf); err != nil {
			return err
		}
	}

	go func() {
		for {
			select {
			case <-time.After(cnf.interval):
				// A sweep that cannot reclaim anything is how a build node runs
				// out of disk, so say so rather than dropping the error.
				if err := client.Clean(ctx, cnf.maxAge, cnf.reference); err != nil {
					logger.Errorf("image cleanup failed: %s", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}
