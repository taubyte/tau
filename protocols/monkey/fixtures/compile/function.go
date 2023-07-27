package compile

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/otiai10/copy"
	"github.com/pterm/pterm"
	"github.com/spf13/afero"
	"github.com/taubyte/config-compiler/common"
	"github.com/taubyte/go-project-schema/functions"
	"github.com/taubyte/go-project-schema/project"
	wasmSpec "github.com/taubyte/go-specs/builders/wasm"
	functionSpec "github.com/taubyte/go-specs/function"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/protocols/monkey/jobs"
	"github.com/taubyte/utils/bundle"
)

type functionContext struct {
	ctx    resourceContext
	config *structureSpec.Function
}

func (ctx resourceContext) function(config *structureSpec.Function) (err error) {
	f := functionContext{
		ctx, config,
	}

	for _, filePath := range ctx.paths {
		fileStat, err := os.Stat(filePath)
		if err != nil {
			return fmt.Errorf("stat of provided path `%s` failed with: %v", ctx.paths, err)
		}

		if fileStat.IsDir() {
			return fmt.Errorf("directory only supported for libraries got: `%s`", ctx.paths)
		}
	}

	for _, _path := range ctx.paths {
		ext := path.Ext(_path)

		if lang := wasmSpec.LangFromExt(ext); lang != nil {
			return f.codeFile(*lang)
		}

		switch ext {
		case ".zwasm":
			return f.zWasmFile()
		case ".wasm":
			return f.wasmFile()
		}
	}

	return errors.New("unsupported file type")
}

func (f functionContext) zWasmFile() error {
	file, err := os.OpenFile(f.ctx.paths[0], os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open provided file `%s` failed with: %s", f.ctx.paths[0], err)
	}
	defer file.Close()

	if f.ctx.call != "" && f.config.Call != f.ctx.call {
		err = f.overrideConfigCall()
		if err != nil {
			return err
		}
	}

	return f.ctx.stashAndPush(f.config.Id, file)
}

func (f functionContext) codeFile(language wasmSpec.SupportedLanguage) error {
	root, err := os.MkdirTemp("/tmp", fmt.Sprintf("%s-*", f.ctx.resourceId))
	if err != nil {
		return err
	}

	c := jobs.Context{
		Node:     f.ctx.universe.TNS().Node(),
		LogFile:  nil,
		WorkDir:  root,
		RepoType: common.CodeRepository,
	}

	copyPath := path.Join(root, functionSpec.PathVariable.String(), f.config.Name)
	for _, filePath := range f.ctx.paths {
		splitPath := strings.Split(filePath, "/")
		filename := splitPath[len(splitPath)-1]

		if err = copy.Copy(filePath, path.Join(copyPath, filename)); err != nil {
			return err
		}
	}

	if err = language.CopyFunctionTemplateConfig(f.ctx.templateRepo, "", copyPath); err != nil {
		return fmt.Errorf("copying `%s` config template failed with: %s", language, err)
	}

	pterm.Info.Println("building function in root:", root)
	c.ForceGitDir(root)
	c.ForceContext(f.ctx.universe.Context())

	p, err := project.Open(project.VirtualFS(afero.NewMemMapFs(), "/"))
	if err != nil {
		return err
	}

	function, err := p.Function(f.config.Name, "")
	if err != nil {
		return err
	}
	function.Set(true, functions.Id(f.ctx.resourceId))

	moduleReader, err := c.HandleOp(jobs.ToOp(function), os.Stdout)
	if err != nil {
		return err
	}

	return f.ctx.stashAndPush(f.ctx.resourceId, moduleReader)
}

// Overrides function "Call" in config
func (f functionContext) overrideConfigCall() error {
	commit, err := f.ctx.simple.TNS().Simple().Commit(f.ctx.projectId, f.ctx.branch)
	if err != nil {
		return err
	}

	path, err := functionSpec.Tns().BasicPath(f.ctx.branch, commit, f.ctx.projectId, f.ctx.applicationId, f.config.Id)
	if err != nil {
		return err
	}

	err = f.ctx.simple.TNS().Push(append(path.Slice(), "call"), f.ctx.call)
	if err != nil {
		return err
	}

	return nil
}

func (f functionContext) wasmFile() error {
	if f.ctx.call != "" && f.config.Call != f.ctx.call {
		err := f.overrideConfigCall()
		if err != nil {
			return err
		}
	}

	out, err := os.CreateTemp("/tmp", "")
	if err != nil {
		return fmt.Errorf("open file failed with %w", err)
	}
	out.Close()

	file, err := bundle.Compress(f.ctx.paths[0], out.Name(), wasmSpec.BufferSize)
	if err != nil {
		return err
	}

	_, err = file.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("seeking `%s`, failed with: %s", f.ctx.paths[0], err)
	}

	return f.ctx.stashAndPush(f.config.Id, file)
}
