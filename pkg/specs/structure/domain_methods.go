package structureSpec

// Object-addressing methods for the tcc-gen'd Domain struct type (see domain.go).

import (
	"github.com/taubyte/tau/pkg/specs/common"
	domainSpec "github.com/taubyte/tau/pkg/specs/domain"
)

func (d Domain) GetName() string {
	return d.Name
}

func (d *Domain) SetId(id string) {
	d.Id = id
}

func (d *Domain) IndexValue(branch, project, app string) (*common.TnsPath, error) {
	return domainSpec.Tns().IndexValue(branch, project, app, d.Id)
}

func (d *Domain) GetId() string {
	return d.Id
}
