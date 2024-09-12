package database

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/services/substrate/components/database/common"
	db "github.com/taubyte/tau/services/substrate/components/database/database"
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

	var (
		ok     bool
		branch string
		commit string
	)

	s.databasesLock.RLock()
	database, ok = s.databases[hash]
	s.databasesLock.RUnlock()
	if !ok {
		context.Config, commit, branch, err = s.fetchConfig(context.ProjectId, context.ApplicationId, context.Matcher)
		if err != nil {
			return nil, fmt.Errorf("getting config for match `%s` failed with: %s", context.Matcher, err)
		}

		// Create new db from config template
		if database, err = db.New(s, s.DBFactory, context); err != nil {
			return nil, fmt.Errorf("creating new database failed with: %s", err)
		}

		s.databasesLock.Lock()
		s.databases[hash] = database
		s.databasesLock.Unlock()

		if err = s.pubsubDatabase(context, branch); err != nil {
			return nil, fmt.Errorf("pubsubDatabase failed with: %w", err)
		}

		s.commitLock.Lock()
		s.commits[hash] = commit
		s.commitLock.Unlock()

		return
	}

	valid, newCommitId, err := s.validateCommit(hash, context.ProjectId, spec.DefaultBranches)
	if err != nil {
		return nil, fmt.Errorf("validating commit failed with: %w", err)
	}

	if !valid {
		s.databasesLock.Lock()
		s.commitLock.Lock()

		defer s.databasesLock.Unlock()
		defer s.commitLock.Unlock()

		database, err = s.updateDatabase(database, spec.DefaultBranches)
		if err != nil {
			return nil, fmt.Errorf("updating database failed with: %w", err)
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
