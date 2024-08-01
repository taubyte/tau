package database

import (
	"fmt"
	"regexp"

	spec "github.com/taubyte/tau/pkg/specs/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (s *Service) fetchConfig(project, application, matcher string) (*structureSpec.Database, string, string, error) {
	// Fetch config from tns and match
	databases, commit, branch, err := s.Tns().Database().All(project, application, spec.DefaultBranches...).List()
	if err != nil {
		return nil, commit, branch, fmt.Errorf("fetching indexed database object failed with: %s", err)
	}

	// Find the config that matches the inputted match
	for _, databaseConfig := range databases {
		if databaseConfig.Regex {
			matched, err := regexp.Match(databaseConfig.Match, []byte(matcher))
			if err != nil {
				return nil, commit, branch, fmt.Errorf("matching regex `%s` with `%s` failed with: %s", matcher, databaseConfig.Match, err)
			}

			if matched {
				return databaseConfig, commit, branch, nil
			}
		} else if !databaseConfig.Regex {
			if matcher == databaseConfig.Match {
				return databaseConfig, commit, branch, nil
			}
		}
	}

	return nil, commit, branch, fmt.Errorf("`%s` did not match with any database", matcher)
}
