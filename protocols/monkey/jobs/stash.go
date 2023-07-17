package jobs

import (
	"context"
	"fmt"
	"io"
	"time"

	hoarderClient "bitbucket.org/taubyte/hoarder/api/p2p"
	"github.com/taubyte/go-interfaces/moody"
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
					logger.Error(moody.Object{"message": fmt.Errorf("creating hoarder client failed with: %s", err)})
					continue
				}

				_, err = hoarder.Stash(cid)
				if err != nil {
					logger.Error(moody.Object{"message": fmt.Errorf("stashing `%s` failed with: %s", cid, err)})
					continue
				}
			}
		}
	}()

	return
}
