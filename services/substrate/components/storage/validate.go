package substrate

import (
	"fmt"

	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

func (s *Service) validateCommit(hash, projectId, branch string) (bool, string, error) {
	s.commitLock.Lock()
	pastCommit, ok := s.commits[hash]
	s.commitLock.Unlock()
	if !ok {
		return false, "", fmt.Errorf("hash `%s` not found in commit cache", hash)
	}

	newCommit, err := s.Tns().Simple().Commit(projectId, branch)
	if err != nil {
		return false, "", err
	}

	if pastCommit != newCommit {
		return false, "", nil
	}

	return true, newCommit, nil

}

func (s *Service) updateStorage(storage storageIface.Storage) (storageIface.Storage, error) {
	config := storage.ContextConfig()
	newConfig, err := s.Tns().Storage().All(config.ProjectId, config.ApplicationId, spec.DefaultBranch).GetByName(config.Config.Name)
	if err != nil {
		return nil, err
	}

	storage.UpdateCapacity(newConfig.Size)

	return storage, nil
}
