package test_utils

import (
	"os"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/pkg/vm/context"
	"github.com/taubyte/tau/services/tns/mocks"
	"github.com/taubyte/utils/id"
)

var (
	TestFunc         structureSpec.Function
	MockConfig       mocks.InjectConfig
	MockGlobalConfig mocks.InjectConfig
	ContextOptions   []context.Option

	TestHost = "ping.examples.tau.link"
	TestPath = "ping"

	Wd string
)

func ResetVars() (err error) {
	TestFunc = structureSpec.Function{
		Id:      id.Generate(),
		Name:    "basic",
		Type:    "http",
		Memory:  10000,
		Timeout: 100000000,
		Method:  "GET",
		Source:  ".",
		Call:    "tou32",
		Paths:   []string{"/ping"},
		Domains: []string{"somDomain"},
	}

	MockConfig = mocks.InjectConfig{
		Branch:      "master",
		Commit:      "head_commit",
		Project:     id.Generate(),
		Application: id.Generate(),
		Cid:         id.Generate(),
	}

	MockGlobalConfig = MockConfig
	MockGlobalConfig.Application = ""

	ContextOptions = []context.Option{
		context.Application(MockConfig.Application),
		context.Project(MockConfig.Project),
		context.Resource(TestFunc.Id),
		context.Branch(MockConfig.Branch),
		context.Commit(MockConfig.Commit),
	}

	if Wd, err = os.Getwd(); err != nil {
		return err
	}

	return nil
}
