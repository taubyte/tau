package jobs

import (
	"fmt"
	"io"

	"github.com/ipfs/go-log/v2"
	_ "github.com/taubyte/builder"
	"github.com/taubyte/config-compiler/compile"
	projectSchema "github.com/taubyte/go-project-schema/project"
	chidori "github.com/taubyte/utils/logger/zap"
)

func (c config) handle() error {
	project, err := projectSchema.Open(projectSchema.SystemFS(c.gitDir))
	if err != nil {
		return fmt.Errorf("opening project failed with: %s", err.Error())
	}

	if project.Get().Id() != c.ProjectID {
		return fmt.Errorf("project ids not equal `%s` != `%s`", c.ProjectID, project.Get().Id())
	}

	rc, err := compile.CompilerConfig(project, c.Job.Meta)
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
	if err = compiler.Build(); err != nil {
		return fmt.Errorf("config compiler build failed with: %s", err.Error())
	}

	if err = compiler.Publish(c.Tns); err != nil {
		return fmt.Errorf("publishing compiled config failed with: %s", err.Error())
	}

	c.LogFile.Seek(0, io.SeekEnd)
	c.LogFile.WriteString(
		chidori.Format(logger, log.LevelInfo, "Successfully written config to tns:\n%v\n", compiler.Object()),
	)

	return nil
}
