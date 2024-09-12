package git

import (
	"fmt"
	"os"
	"path"
	"strings"
)

func (c *Repository) handle_ephemeral() (err error) {
	if strings.HasPrefix(c.root, "/") {
		return fmt.Errorf("root: `%s` must be relative when using Temporary()", c.root)
	}

	c.workDir, err = os.MkdirTemp("", "repo-*")
	c.root = path.Join(c.workDir, c.root)
	if err != nil {
		return fmt.Errorf(
			"creating new git client (url=%s) failed with %v",
			c.url,
			err,
		)
	}
	// make root dir if needed
	if len(c.workDir) > 0 {
		err = os.MkdirAll(c.root, 0755)
		if err != nil {
			return fmt.Errorf("failed making root dir `%s` with %v", c.root, err)
		}
	} else {
		c.workDir = c.root
	}

	// make sure we cleanup
	go func() {
		<-c.ctx.Done()
		if !c.ephemeralNoDelete {
			os.RemoveAll(c.workDir)
		}
	}()

	return nil
}
