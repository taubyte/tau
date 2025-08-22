package project

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/common"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func (p *project) WrapError(format string, i ...any) error {
	return fmt.Errorf("on project `"+p.Get().Name()+"`; "+format, i...)
}

// Not needed as we are overriding Root
func (p *project) Directory() string {
	return ""
}

func (p *project) Name() string {
	return p.Get().Name()
}

// A project cannot have a parent so we return an empty string to satisfy the required interface
func (p *project) AppName() string {
	return ""
}

// Config overrides basic.Config because project config is within a folder
func (p *project) Config() *seer.Query {
	return p.Root().Document()
}

// Root overrides basic.Root
func (p *project) Root() *seer.Query {
	return p.seer.Get(common.ConfigFileName)
}

func (p *project) SetName(name string) {
	p.Set(true, Name(name))
}
