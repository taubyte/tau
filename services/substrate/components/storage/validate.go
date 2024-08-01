package substrate

import (
	"fmt"

	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

func (s *Service) validateCommit(hash, projectId string) (bool, string, string, error) {
	s.commitLock.Lock()
	pastCommit, ok := s.commits[hash]
	s.commitLock.Unlock()
	if !ok {
		return false, "", "", fmt.Errorf("hash `%s` not found in commit cache", hash)
	}

	newCommit, branch, err := s.Tns().Simple().Commit(projectId, spec.DefaultBranches...)
	if err != nil {
		return false, "", branch, err
	}

	if pastCommit != newCommit {
		return false, "", branch, nil
	}

	return true, newCommit, branch, nil

}

func (s *Service) updateStorage(storage storageIface.Storage, branch string) (storageIface.Storage, error) {
	config := storage.ContextConfig()
	newConfig, err := s.Tns().Storage().All(config.ProjectId, config.ApplicationId, spec.DefaultBranches...).GetByName(config.Config.Name)
	if err != nil {
		return nil, err
	}

	storage.UpdateCapacity(newConfig.Size)

	return storage, nil
}
