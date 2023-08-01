package database

import (
	"fmt"
	"regexp"

	spec "github.com/taubyte/go-specs/common"
	structureSpec "github.com/taubyte/go-specs/structure"
)

func (s *Service) fetchConfig(project, application, matcher string) (*structureSpec.Database, error) {
	// Fetch config from tns and match
	databases, err := s.Tns().Database().All(project, application, spec.DefaultBranch).List()
	if err != nil {
		return nil, fmt.Errorf("fetching indexed database object failed with: %s", err)
	}

	// Find the config that matches the inputted match
	for _, databaseConfig := range databases {
		if databaseConfig.Regex {
			matched, err := regexp.Match(databaseConfig.Match, []byte(matcher))
			if err != nil {
				return nil, fmt.Errorf("matching regex `%s` with `%s` failed with: %s", matcher, databaseConfig.Match, err)
			}

			if matched {
				return databaseConfig, nil
			}
		} else if !databaseConfig.Regex {
			if matcher == databaseConfig.Match {
				return databaseConfig, nil
			}
		}
	}

	return nil, fmt.Errorf("`%s` did not match with any database", matcher)
}
