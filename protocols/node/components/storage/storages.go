package service

import (
	"context"
	"fmt"
	"regexp"

	storageIface "github.com/taubyte/go-interfaces/services/substrate/storage"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/node/components/storage/common"
)

func (s *Service) Storages() map[string]storageIface.Storage {
	return s.storages
}

func (s *Service) Get(context storageIface.Context) (storageIface.Storage, error) {
	hash, err := common.GetStorageHash(context)
	if err != nil {
		return nil, err
	}

	storage, ok := s.storages[hash]
	if !ok {
		return nil, fmt.Errorf("unable to find storage for given context: %v", context)
	}

	return storage, nil
}

func (s *Service) getStoreConfig(project, application, matcher string) (*structureSpec.Storage, error) {
	storages, err := s.Tns().Storage().All(project, application, s.Branch()).List()
	if err != nil {
		return nil, fmt.Errorf("listing storage configs failed with: %s", err)
	}

	// Find the config that matches the inputted match
	for _, storageConfig := range storages {
		if storageConfig.Regex {
			matched, err := regexp.Match(storageConfig.Match, []byte(matcher))
			if err != nil {
				return nil, fmt.Errorf("matching regex `%s` with `%s` failed with: %s", matcher, storageConfig.Match, err)
			}

			if matched {
				return storageConfig, nil
			}
		} else if !storageConfig.Regex {
			if matcher == storageConfig.Match {
				return storageConfig, nil
			}
		}
	}

	return nil, fmt.Errorf("`%s` did not match with any storages", matcher)
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}
