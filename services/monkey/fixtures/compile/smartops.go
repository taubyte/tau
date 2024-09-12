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
	"github.com/taubyte/tau/pkg/schema/project"
	smartopsLib "github.com/taubyte/tau/pkg/schema/smartops"
	wasmSpec "github.com/taubyte/tau/pkg/specs/builders/wasm"
	smartopsSpec "github.com/taubyte/tau/pkg/specs/smartops"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/monkey/jobs"
	"github.com/taubyte/utils/bundle"
)

type smartopsContext struct {
	ctx    resourceContext
	config *structureSpec.SmartOp
}

func (ctx resourceContext) smartops(config *structureSpec.SmartOp) (err error) {
	f := smartopsContext{
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

func (f smartopsContext) zWasmFile() error {
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

func (f smartopsContext) codeFile(language wasmSpec.SupportedLanguage) error {
	root, err := os.MkdirTemp(os.TempDir(), fmt.Sprintf("%s-*", f.ctx.resourceId))
	if err != nil {
		return err
	}

	c := jobs.Context{
		Node:    f.ctx.universe.TNS().Node(),
		LogFile: nil,
		WorkDir: root,
		Monkey: fakeMonkey{
			hoarderClient: f.ctx.hoarderClient,
		},
		GeneratedDomainRegExp: generatedDomainRegExp,
	}

	copyPath := path.Join(root, smartopsSpec.PathVariable.String(), f.config.Name)
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

	pterm.Info.Println("building smartops in root:", root)
	c.ForceGitDir(root)
	c.ForceContext(f.ctx.universe.Context())

	p, err := project.Open(project.VirtualFS(afero.NewMemMapFs(), "/"))
	if err != nil {
		return err
	}

	smartops, err := p.SmartOps(f.config.Name, "")
	if err != nil {
		return err
	}
	smartops.Set(true, smartopsLib.Id(f.ctx.resourceId))

	moduleReader, err := c.HandleOp(jobs.ToOp(smartops), os.Stdout)
	if err != nil {
		return err
	}

	return f.ctx.stashAndPush(f.ctx.resourceId, moduleReader)
}

// Overrides smartops "Call" in config
func (f smartopsContext) overrideConfigCall() error {
	tns, err := f.ctx.simple.TNS()
	if err != nil {
		return err
	}

	commit, branch, err := tns.Simple().Commit(f.ctx.projectId, f.ctx.branch)
	if err != nil {
		return err
	}

	path, err := smartopsSpec.Tns().BasicPath(branch, commit, f.ctx.projectId, f.ctx.applicationId, f.config.Id)
	if err != nil {
		return err
	}

	err = tns.Push(append(path.Slice(), "call"), f.ctx.call)
	if err != nil {
		return err
	}

	return nil
}

func (f smartopsContext) wasmFile() error {
	if f.ctx.call != "" && f.config.Call != f.ctx.call {
		err := f.overrideConfigCall()
		if err != nil {
			return err
		}
	}

	out, err := os.CreateTemp("", "")
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
