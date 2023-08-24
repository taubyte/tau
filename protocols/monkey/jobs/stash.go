package jobs

import (
	"context"
	"fmt"
	"io"
	"time"

	hoarderClient "github.com/taubyte/tau/clients/p2p/hoarder"
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
					logger.Error("creating hoarder client failed with:", err.Error())
					continue
				}

				_, err = hoarder.Stash(cid)
				if err != nil {
					logger.Errorf("stashing `%s` failed with: %s", cid, err.Error())
					continue
				}
			}
		}
	}()

	return
}
