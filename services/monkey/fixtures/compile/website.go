package compile

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/otiai10/copy"
	"github.com/pterm/pterm"
	iface "github.com/taubyte/tau/core/builders"
	builder "github.com/taubyte/tau/pkg/builder"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/monkey/jobs"
)

type websiteContext struct {
	ctx    resourceContext
	config *structureSpec.Website
}

func (ctx resourceContext) website(config *structureSpec.Website) (err error) {
	w := websiteContext{
		ctx, config,
	}

	fileStat, err := os.Stat(ctx.paths[0])
	if err != nil {
		return
	}

	if fileStat.IsDir() {
		return w.directory()

	}

	if strings.HasSuffix(ctx.paths[0], ".zip") {
		return w.zip()
	}

	return fmt.Errorf("website must be a directory or zip file got: `%s`", ctx.paths)
}

func (w websiteContext) zip() error {
	file, err := os.OpenFile(w.ctx.paths[0], os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open provided file `%s` failed with: %s", w.ctx.paths, err)
	}
	defer file.Close()

	return w.ctx.stashAndPush(w.config.Id, file)
}

func (w websiteContext) directory() error {
	return w.ctx.stashCached(w.ctx.resourceId, "zip", func() (io.ReadSeekCloser, error) {
		root, err := os.MkdirTemp(os.TempDir(), fmt.Sprintf("%s-*", w.ctx.resourceId))
		if err != nil {
			return nil, err
		}

		err = copy.Copy(w.ctx.paths[0], root)
		if err != nil {
			return nil, err
		}

		pterm.Info.Println("building website in root:", root)

		c := jobs.Context{
			Node:    w.ctx.universe.TNS().Node(),
			LogFile: os.Stdout,
			WorkDir: root,
			Monkey: fakeMonkey{
				hoarderClient: w.ctx.hoarderClient,
			},
			GeneratedDomainRegExp: generatedDomainRegExp,
		}

		c.ForceGitDir(w.ctx.paths[0])
		c.ForceContext(w.ctx.universe.Context())

		b, err := builder.New(w.ctx.universe.Context(), c.LogFile, c.WorkDir)
		if err != nil {
			return nil, fmt.Errorf("builder new failed with: %s", err)
		}

		asset, err := b.Build(b.Wd().Website().SetWorkDir())
		if err != nil {
			return nil, err
		}

		return asset.Compress(iface.Website)
	})
}
