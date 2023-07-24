package jobs

import (
	"context"
	"fmt"
	"io"
	"time"

	hoarderClient "github.com/taubyte/odo/clients/p2p/hoarder"
)

func (c Context) StashBuildFile(zip io.ReadSeekCloser) (cid string, err error) {
	cid, err = c.Node.AddFile(zip)
	if err != nil {
		err = fmt.Errorf("adding build file to node failed with: %s", err)
		return
	}

	go func() {
		ctx, ctxC := context.WithTimeout(c.ctx, 10*time.Minute)
		defer ctxC()

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
				hoarder, err := hoarderClient.New(ctx, c.OdoClientNode)
				if err != nil {
					logger.Errorf("creating hoarder client failed with: %w", err)
					continue
				}

				_, err = hoarder.Stash(cid)
				if err != nil {
					logger.Error("stashing `%s` failed with: %w", cid, err)
					continue
				}
			}
		}
	}()

	return
}
