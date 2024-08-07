package substrate

import (
	"fmt"
	"regexp"

	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	spec "github.com/taubyte/tau/pkg/specs/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/storage/common"
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

func (s *Service) getStoreConfig(project, application, matcher string) (*structureSpec.Storage, string, string, error) {
	storages, commit, branch, err := s.Tns().Storage().All(project, application, spec.DefaultBranches...).List()
	if err != nil {
		return nil, commit, branch, fmt.Errorf("listing storage configs failed with: %s", err)
	}

	// Find the config that matches the inputted match
	for _, storageConfig := range storages {
		if storageConfig.Regex {
			matched, err := regexp.Match(storageConfig.Match, []byte(matcher))
			if err != nil {
				return nil, commit, branch, fmt.Errorf("matching regex `%s` with `%s` failed with: %s", matcher, storageConfig.Match, err)
			}

			if matched {
				return storageConfig, commit, branch, nil
			}
		} else if !storageConfig.Regex {
			if matcher == storageConfig.Match {
				return storageConfig, commit, branch, nil
			}
		}
	}

	return nil, commit, branch, fmt.Errorf("`%s` did not match with any storages", matcher)
}
