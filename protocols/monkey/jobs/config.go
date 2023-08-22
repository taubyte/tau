package jobs

import (
	"github.com/ipfs/go-log/v2"
	_ "github.com/taubyte/builder"
	"github.com/taubyte/config-compiler/compile"
	projectSchema "github.com/taubyte/go-project-schema/project"
)

func (c config) handle() error {
	project, err := projectSchema.Open(projectSchema.SystemFS(c.gitDir))
	if err != nil {
		return c.logErrorHandler("opening project failed with: %s", err.Error())
	}

	if project.Get().Id() != c.ProjectID {
		return c.logErrorHandler("project ids not equal `%s` != `%s`", c.ProjectID, project.Get().Id())
	}

	rc, err := compile.CompilerConfig(project, c.Job.Meta)
	if err != nil {
		return c.logErrorHandler("compiling project failed with: %s", err.Error())
	}

	compileOps := []compile.Option{}
	if c.Monkey.Dev() {
		compileOps = append(compileOps, compile.Dev())
	} else {
		compileOps = append(compileOps, compile.DVKey(c.DVPublicKey))

	}

	compiler, err := compile.New(rc, compileOps...)
	if err != nil {
		return c.logErrorHandler("new config compiler failed with: %s", err.Error())
	}

	defer compiler.Close()
	if err = compiler.Build(); err != nil {
		return c.logErrorHandler("config compiler build failed with: %s", err.Error())
	}

	if err = compiler.Publish(c.Tns); err != nil {
		return c.logErrorHandler("publishing compiled config failed with: %s", err.Error())
	}

	c.addDebugMsg(log.LevelInfo, "Successfully written config to tns:\n%v\n", compiler.Object())

	return nil
}
