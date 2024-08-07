package fixtures

import (
	"regexp"

	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	"github.com/taubyte/tau/pkg/schema/project"
)

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

func inject(project project.Project, simple *dream.Simple) error {
	fakeMeta := commonTest.ConfigRepo.HookInfo
	fakeMeta.Repository.Provider = "github"
	fakeMeta.Repository.Branch = "main"
	fakeMeta.HeadCommit.ID = "testCommit"
	rc, err := compile.CompilerConfig(project, fakeMeta, generatedDomainRegExp)
	if err != nil {
		return err
	}

	compiler, err := compile.New(rc, compile.Dev())
	if err != nil {
		return err
	}
	defer compiler.Close()

	err = compiler.Build()
	if err != nil {
		return err
	}

	tns, err := simple.TNS()
	if err != nil {
		return err
	}

	// publish ( compile & send to TNS )
	err = compiler.Publish(tns)
	if err != nil {
		return err
	}

	return nil
}
