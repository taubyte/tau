package gc

import (
	"context"
	"time"

	"github.com/docker/docker/api/types/filters"
	ci "github.com/taubyte/tau/pkg/containers"
)

type config struct {
	interval time.Duration
	maxAge   time.Duration
	filters  filters.Args
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
		defer client.Close()
		for {
			select {
			case <-time.After(cnf.interval):
				client.Clean(ctx, cnf.maxAge, cnf.filters)
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}
