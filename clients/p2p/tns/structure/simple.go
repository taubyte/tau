package structure

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func (s *simpleClient) Project(projectID string, branches ...string) (interface{}, error) {
	commit, branch, err := s.Commit(projectID, branches...)
	if err != nil {
		return nil, err
	}

	projectObj, err := s.Structure.tns.Fetch(
		methods.ProjectPrefix(
			projectID,
			branch,
			commit,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching project object failed: %w", err)
	}

	return projectObj.Interface(), nil
}

func (s *simpleClient) GetRepositoryProjectId(gitProvider, repoId string) (projectId string, err error) {
	queryKey := []string{string(methods.RepositoryPathVariable), gitProvider, repoId}
	resp, err := s.Structure.tns.Lookup(tns.Query{Prefix: queryKey})
	if err != nil {
		err = fmt.Errorf("lookup repository `%s` failed with: %s", repoId, err)
		return
	}

	respArr, ok := resp.([]string)
	if !ok || len(respArr) == 0 {
		err = fmt.Errorf("response from lookup repository `%s` not an array or empty: `%v`", repoId, resp)
		return
	}

	for _, key := range respArr {
		if strings.Contains(key, "/type") {
			repoInfo := strings.Split(key, "/")
			if len(repoInfo) < 3 {
				err = fmt.Errorf("invalid project key when getting /type for repository `%s`: %s", repoId, key)
				return
			}
			projectId = repoInfo[len(repoInfo)-2]
		}
	}

	return
}
