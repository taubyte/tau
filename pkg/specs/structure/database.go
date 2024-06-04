package structureSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	databaseSpec "github.com/taubyte/tau/pkg/specs/database"
)

type Database struct {
	Id          string
	Name        string
	Description string
	Tags        []string

	Match string
	Regex bool `mapstructure:"useRegex"`
	Local bool
	Key   string
	Min   int
	Max   int
	Size  uint64

	// noset, this is parsed from the tags
	SmartOps []string

	Basic
	Indexer
}

func (d Database) GetName() string {
	return d.Name
}

func (d *Database) SetId(id string) {
	d.Id = id
}

func (d *Database) BasicPath(branch, commit, project, app string) (*common.TnsPath, error) {
	return databaseSpec.Tns().BasicPath(branch, commit, project, app, d.Id)
}

func (d *Database) IndexValue(branch, project, app string) (*common.TnsPath, error) {
	return databaseSpec.Tns().IndexValue(branch, project, app, d.Id)
}

func (d *Database) IndexPath(project, app string) *common.TnsPath {
	return databaseSpec.Tns().IndexPath(project, app, d.Name)
}

func (d *Database) GetId() string {
	return d.Id
}
