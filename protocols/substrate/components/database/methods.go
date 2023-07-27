package database

import (
	"context"
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	"github.com/taubyte/tau/protocols/substrate/components/database/common"
	db "github.com/taubyte/tau/protocols/substrate/components/database/database"
)

func (s *Service) Cache() components.Cache {
	return nil
}

func (s *Service) CheckTns(components.MatchDefinition) ([]components.Serviceable, error) {
	return nil, nil
}

func (s *Service) Database(context iface.Context) (database iface.Database, err error) {
	hash, err := common.GetDatabaseHash(context)
	if err != nil {
		return nil, fmt.Errorf("getting database hash for `%s` failed with: %s", context.Matcher, err)
	}

	var ok bool
	s.databasesLock.RLock()
	database, ok = s.databases[hash]
	s.databasesLock.RUnlock()
	if !ok {
		if context.Config, err = s.fetchConfig(context.ProjectId, context.ApplicationId, context.Matcher); err != nil {
			return nil, fmt.Errorf("getting config for match `%s` failed with: %s", context.Matcher, err)
		}

		// Create new db from config template
		if database, err = db.New(s, s.DBFactory, context); err != nil {
			return nil, fmt.Errorf("creating new database failed with: %s", err)
		}

		s.databasesLock.Lock()
		s.databases[hash] = database
		s.databasesLock.Unlock()

		if err = s.pubsubDatabase(context, s.Branch()); err != nil {
			return nil, err
		}

		var commit string
		commit, err = s.Tns().Simple().Commit(context.ProjectId, s.Branch())
		if err != nil {
			return nil, fmt.Errorf("getting commit for project id `%s` and branch `%s` failed with: %s", context.ProjectId, s.Branch(), err)
		}

		s.commitLock.Lock()
		s.commits[hash] = commit
		s.commitLock.Unlock()

		return
	}

	valid, newCommitId, err := s.validateCommit(hash, context.ProjectId, s.Branch())
	if err != nil {
		return nil, err
	}

	if !valid {
		s.databasesLock.Lock()
		s.commitLock.Lock()

		defer s.databasesLock.Unlock()
		defer s.commitLock.Unlock()

		database, err = s.updateDatabase(database)
		if err != nil {
			return nil, err
		}

		s.databases[hash] = database
		s.commits[hash] = newCommitId
	}

	return
}

func (s *Service) Databases() map[string]iface.Database {
	return s.databases
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}
