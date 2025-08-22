package domains

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/common"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func (d *domain) WrapError(format string, i ...any) error {
	return fmt.Errorf("on domain `"+d.name+"`; "+format, i...)
}

func (d *domain) Name() string {
	return d.name
}

func (d *domain) Root() *seer.Query {
	return d.Resource.Root()
}

func (d *domain) Config() *seer.Query {
	return d.Resource.Config()
}

func (d *domain) AppName() string {
	return d.application
}

func (*domain) Directory() string {
	return common.DomainFolder
}

func (d *domain) SetName(name string) {
	d.name = name
}
