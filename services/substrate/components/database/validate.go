package database

import (
	"fmt"

	iface "github.com/taubyte/tau/core/services/substrate/components/database"
)

func (s *Service) validateCommit(hash, projectId string, branches []string) (bool, string, error) {
	s.commitLock.Lock()
	pastCommit, ok := s.commits[hash]
	s.commitLock.Unlock()
	if !ok {
		return false, "", fmt.Errorf("hash `%s` not found in commit cache", hash)
	}

	newCommit, _, err := s.Tns().Simple().Commit(projectId, branches...)
	if err != nil {
		return false, "", err
	}

	if pastCommit != newCommit {
		return false, "", nil
	}

	return true, newCommit, nil

}

func (s *Service) updateDatabase(database iface.Database, branches []string) (iface.Database, error) {
	config := database.DBContext()
	newConfig, err := s.Tns().Database().All(config.ProjectId, config.ApplicationId, branches...).GetByName(config.Config.Name)
	if err != nil {
		return nil, err
	}

	database.KV().UpdateSize(newConfig.Size)

	return database, nil
}
