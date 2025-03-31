package jobs

import (
	"fmt"
	"io"

	_ "github.com/taubyte/tau/pkg/builder"
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func (c config) handle() error {
	project, err := projectSchema.Open(projectSchema.SystemFS(c.gitDir))
	if err != nil {
		return fmt.Errorf("opening project failed with: %s", err.Error())
	}

	if project.Get().Id() != c.ProjectID {
		return fmt.Errorf("project ids not equal `%s` != `%s`", c.ProjectID, project.Get().Id())
	}

	rc, err := compile.CompilerConfig(project, c.Job.Meta, c.GeneratedDomainRegExp)
	if err != nil {
		return fmt.Errorf("compiling project failed with: %s", err.Error())
	}

	compileOps := []compile.Option{}
	if c.Monkey.Dev() {
		compileOps = append(compileOps, compile.Dev())
	} else {
		compileOps = append(compileOps, compile.DVKey(c.DVPublicKey))

	}

	compiler, err := compile.New(rc, compileOps...)
	if err != nil {
		return fmt.Errorf("new config compiler failed with: %s", err.Error())
	}

	defer compiler.Close()
	defer func() {
		compiler.Logs().Seek(0, io.SeekStart)
		io.Copy(c.LogFile, compiler.Logs())
	}()

	if err = compiler.Build(); err != nil {
		return fmt.Errorf("config compiler build failed with: %s", err.Error())
	}

	if err = compiler.Publish(c.Tns); err != nil {
		return fmt.Errorf("publishing compiled config failed with: %s", err.Error())
	}

	return nil
}
