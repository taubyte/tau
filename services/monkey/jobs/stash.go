package jobs

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Retries for pushing a build to Hoarder. The first few are quick, inside the
// job; the rest run in the background on the node's context, because the job's
// context ends with the job and a retry tied to it never ran.
var (
	StashQuickRetries  = 3
	StashQuickInterval = time.Second
	StashRetryInterval = 10 * time.Second
	StashRetryFor      = 10 * time.Minute
)

func (c Context) StashBuildFile(zip io.ReadSeekCloser) (cid string, err error) {
	cid, err = c.Node.AddFile(zip)
	if err != nil {
		err = fmt.Errorf("adding build file to node failed with: %s", err)
		return
	}

	// Push bytes from our own node (re-opened per attempt) rather than holding
	// the caller's reader.
	for i := 0; i < StashQuickRetries; i++ {
		if i > 0 {
			time.Sleep(StashQuickInterval)
		}
		e := c.pushStash(c.ctx, cid)
		if e == nil {
			return
		}
		logger.Warnf("stashing `%s` failed (attempt %d) with: %s", cid, i+1, e)
	}

	// Until a hoarder holds it, the module lives only on this node; keep trying
	// past the job, and say so if it never lands.
	go func() {
		ctx, ctxC := context.WithTimeout(c.Node.Context(), StashRetryFor)
		defer ctxC()
		for {
			select {
			case <-ctx.Done():
				logger.Errorf("stashing `%s` gave up after %s; only this node holds it", cid, StashRetryFor)
				return
			case <-time.After(StashRetryInterval):
				if e := c.pushStash(ctx, cid); e != nil {
					logger.Warnf("stashing `%s` failed with: %s", cid, e)
					continue
				}
				logger.Infof("stashing `%s` succeeded", cid)
				return
			}
		}
	}()

	return
}

// pushStash streams the CID's bytes from our node to a hoarder.
func (c Context) pushStash(ctx context.Context, cid string) error {
	f, err := c.Node.GetFile(ctx, cid)
	if err != nil {
		return fmt.Errorf("re-opening %s failed with: %w", cid, err)
	}
	defer f.Close()
	return c.Monkey.Hoarder().Stash(cid, f)
}
