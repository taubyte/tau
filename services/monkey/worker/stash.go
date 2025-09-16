package worker

import (
	"context"
	"fmt"
	"io"
	"time"
)

func (c instance) StashBuildFile(zip io.ReadSeekCloser) (cid string, err error) {
	cid, err = c.Node.AddFile(zip)
	if err != nil {
		err = fmt.Errorf("adding build file to node failed with: %s", err)
		return
	}

	if _, err = c.Monkey.Hoarder().Stash(cid); err != nil {
		err = nil
		go func() {
			ctx, ctxC := context.WithTimeout(c.ctx, 10*time.Minute)
			defer ctxC()

			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):

					_, err = c.Monkey.Hoarder().Stash(cid)
					if err != nil {
						logger.Errorf("stashing `%s` failed with: %s", cid, err.Error())
						continue
					} else {
						logger.Infof("stashing `%s` suceeded", cid)
						return
					}
				}
			}
		}()
	}

	return
}
