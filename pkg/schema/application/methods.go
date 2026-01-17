package application

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/common"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func (a *application) WrapError(format string, i ...any) error {
	return fmt.Errorf("on application `"+a.name+"`; "+format, i...)
}

func (a *application) Directory() string {
	return common.ApplicationFolder
}

func (a *application) Name() string {
	return a.name
}

// An application cannot have a parent so we return an empty string to satisfy the required interface
func (a *application) AppName() string {
	return ""
}

// Config overrides basic.Config because application config is within a folder
func (a *application) Config() *seer.Query {
	return a.Root().Get(common.ConfigFileName).Document()
}

func (a *application) Root() *seer.Query {
	return a.seer.Get(a.Directory()).Get(a.name)
}

func (a *application) SetName(name string) {
	a.name = name
}
