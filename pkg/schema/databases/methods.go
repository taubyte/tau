package databases

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/common"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func (d *database) WrapError(format string, i ...any) error {
	return fmt.Errorf("on database `"+d.name+"`; "+format, i...)
}

func (d *database) Name() string {
	return d.name
}

func (d *database) AppName() string {
	return d.application
}

func (d *database) Root() *seer.Query {
	return d.Resource.Root()
}

func (d *database) Config() *seer.Query {
	return d.Resource.Config()
}

func (d *database) Directory() string {
	return common.DatabaseFolder
}

func (d *database) SetName(name string) {
	d.name = name
}
