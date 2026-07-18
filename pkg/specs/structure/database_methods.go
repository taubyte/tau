package structureSpec

// Object-addressing methods for the tcc-gen'd Database struct type (see database.go).

import (
	"github.com/taubyte/tau/pkg/specs/common"
	databaseSpec "github.com/taubyte/tau/pkg/specs/database"
)

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
