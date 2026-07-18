package structureSpec

// Object-addressing methods for the tcc-gen'd Storage struct type (see storage.go).

import (
	"github.com/taubyte/tau/pkg/specs/common"
	storageSpec "github.com/taubyte/tau/pkg/specs/storage"
)

func (s Storage) GetName() string {
	return s.Name
}

func (s *Storage) SetId(id string) {
	s.Id = id
}

func (s *Storage) BasicPath(branch, commit, project, app string) (*common.TnsPath, error) {
	return storageSpec.Tns().BasicPath(branch, commit, project, app, s.Id)
}

func (s *Storage) IndexValue(branch, project, app string) (*common.TnsPath, error) {
	return storageSpec.Tns().IndexValue(branch, project, app, s.Id)
}

func (s *Storage) IndexPath(project, app string) *common.TnsPath {
	return storageSpec.Tns().IndexPath(project, app, s.Name)
}

func (s *Storage) GetId() string {
	return s.Id
}
