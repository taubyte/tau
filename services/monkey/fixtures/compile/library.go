package compile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/otiai10/copy"
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/core/builders"
	wasmSpec "github.com/taubyte/tau/pkg/specs/builders/wasm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/monkey/jobs"
)

type libraryContext struct {
	ctx    resourceContext
	config *structureSpec.Library
}

func (ctx resourceContext) library(config *structureSpec.Library) (err error) {
	l := libraryContext{
		ctx, config,
	}

	if ctx.call != "" {
		return fmt.Errorf("call is not used with a library, edit the function call `library.call`")
	}

	stat, err := os.Stat(ctx.paths[0])
	if err != nil {
		return fmt.Errorf("stat of provided path `%s` failed with: %v", ctx.paths, err)
	}

	if stat.IsDir() {
		return l.directory()
	}

	if strings.HasSuffix(ctx.paths[0], ".zwasm") {
		return l.wasmFile()
	}

	return fmt.Errorf("invalid path expected zwasm or directory: %s", ctx.paths)
}

func (l libraryContext) directory() error {
	return l.ctx.stashCached(l.ctx.resourceId, "zwasm", func() (io.ReadSeekCloser, error) {
		root, err := os.MkdirTemp(os.TempDir(), fmt.Sprintf("%s-*", l.ctx.resourceId))
		if err != nil {
			return nil, err
		}

		c := jobs.Context{
			Node:    l.ctx.universe.TNS().Node(),
			LogFile: os.Stdout,
			WorkDir: root,
			Monkey: fakeMonkey{
				hoarderClient: l.ctx.hoarderClient,
			},
			GeneratedDomainRegExp: generatedDomainRegExp,
		}

		var language *wasmSpec.SupportedLanguage

		for _, filePath := range l.ctx.paths {
			fileStat, err := os.Stat(filePath)
			if err != nil {
				return nil, err
			}

			if fileStat.IsDir() {
				files, err := os.ReadDir(filePath)
				if err != nil {
					return nil, err
				}
				for _, file := range files {
					name := file.Name()
					if lang := wasmSpec.LangFromExt(path.Ext(name)); lang != nil {
						language = lang
					}

					copy.Copy(path.Join(filePath, file.Name()), path.Join(root, file.Name()))
				}
			} else {
				return nil, fmt.Errorf("expected path  `%s` to be directory", filePath)
			}
		}

		if language == nil {
			return nil, errors.New("library includes unsupported code files")
		}

		if err := copyTemplateConfig(*language, root); err != nil {
			return nil, err
		}

		pterm.Info.Println("building library in root:", root)
		c.ForceGitDir(root)
		c.ForceContext(l.ctx.universe.Context())

		asset, err := c.HandleLibrary()
		if err != nil {
			return nil, err
		}

		return asset.Compress(builders.WASM)
	})
}

func (l libraryContext) wasmFile() error {
	file, err := os.OpenFile(l.ctx.paths[0], os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open provided file `%s` failed with: %s", l.ctx.paths, err)
	}
	defer file.Close()

	return l.ctx.stashAndPush(l.config.Id, file)
}
