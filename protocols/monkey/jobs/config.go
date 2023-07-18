package jobs

import (
	"fmt"

	"bitbucket.org/taubyte/config-compiler/compile"
	_ "github.com/taubyte/builder"
	projectSchema "github.com/taubyte/go-project-schema/project"
)

func (c config) handle() (err error) {
	project, err := projectSchema.Open(projectSchema.SystemFS(c.gitDir))
	if err != nil {
		return
	}

	if project.Get().Id() != c.ProjectID {
		return fmt.Errorf("project ids not equal `%s` != `%s`", c.ProjectID, project.Get().Id())
	}

	rc, err := compile.CompilerConfig(project, c.Job.Meta)
	if err != nil {
		return
	}

	compileOps := []compile.Option{}
	if c.Monkey.Dev() {
		compileOps = append(compileOps, compile.Dev())
	} else {
		compileOps = append(compileOps, compile.DVKey(c.DVPublicKey))

	}

	compiler, err := compile.New(rc, compileOps...)
	if err != nil {
		return
	}

	defer compiler.Close()
	err = compiler.Build()
	if err != nil {
		return
	}

	return compiler.Publish(c.Tns)
}
