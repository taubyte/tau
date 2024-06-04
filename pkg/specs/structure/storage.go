package structureSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	storageSpec "github.com/taubyte/tau/pkg/specs/storage"
)

type Storage struct {
	Id          string
	Name        string
	Description string
	Tags        []string

	Match      string
	Regex      bool `mapstructure:"useRegex"`
	Type       string
	Public     bool
	Size       uint64
	Ttl        uint64
	Versioning bool

	// noset, this is parsed from the tags
	SmartOps []string

	Basic
	Indexer
}

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
