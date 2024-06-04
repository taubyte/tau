package structureSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	domainSpec "github.com/taubyte/tau/pkg/specs/domain"
)

type Domain struct {
	Id          string
	Name        string
	Description string
	Tags        []string

	Fqdn     string
	CertType string `mapstructure:"cert-type"`
	CertFile string `mapstructure:"cert-file"`
	KeyFile  string `mapstructure:"key-file"`

	// noset, this is parsed from the tags
	SmartOps []string

	Indexer
}

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
