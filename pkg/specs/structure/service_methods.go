package structureSpec

// Object-addressing methods for the tcc-gen'd Service struct type (see service.go).

import (
	"github.com/taubyte/tau/pkg/specs/common"
	serviceSpec "github.com/taubyte/tau/pkg/specs/service"
)

func (s Service) GetName() string {
	return s.Name
}

func (s *Service) SetId(id string) {
	s.Id = id
}

func (s *Service) IndexValue(branch, projectId, appId string) (*common.TnsPath, error) {
	return serviceSpec.Tns().IndexValue(branch, projectId, appId, s.Id)
}

func (s *Service) EmptyPath(branch, commit, projectId, appId string) (*common.TnsPath, error) {
	return serviceSpec.Tns().EmptyPath(branch, commit, projectId, appId)
}

func (s *Service) GetId() string {
	return s.Id
}
