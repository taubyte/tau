package jobs

import (
	"context"
	"fmt"
	"io"
	"time"
)

func (c Context) StashBuildFile(zip io.ReadSeekCloser) (cid string, err error) {
	cid, err = c.Node.AddFile(zip)
	if err != nil {
		err = fmt.Errorf("adding build file to node failed with: %s", err)
		return
	}

	// Push bytes from our own node (re-opened per attempt) rather than holding
	// the caller's reader — the retry goroutine outlives it.
	if err = c.pushStash(cid); err != nil {
		err = nil
		go func() {
			ctx, ctxC := context.WithTimeout(c.ctx, 10*time.Minute)
			defer ctxC()

			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
					if e := c.pushStash(cid); e != nil {
						logger.Errorf("stashing `%s` failed with: %s", cid, e.Error())
						continue
					}
					logger.Infof("stashing `%s` succeeded", cid)
					return
				}
			}
		}()
	}

	return
}

// pushStash streams the CID's bytes from our node to a hoarder.
func (c Context) pushStash(cid string) error {
	f, err := c.Node.GetFile(c.ctx, cid)
	if err != nil {
		return fmt.Errorf("re-opening %s failed with: %w", cid, err)
	}
	defer f.Close()
	return c.Monkey.Hoarder().Stash(cid, f)
}
