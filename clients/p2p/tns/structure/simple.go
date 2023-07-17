package structure

import (
	"fmt"
	"strings"

	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-specs/methods"
)

func (s *simpleClient) Project(projectID, branch string) (interface{}, error) {
	commit, err := s.Commit(projectID, branch)
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
		return nil, fmt.Errorf("Fetching project object failed: %w", err)
	}

	return projectObj.Interface(), nil
}

func (s *simpleClient) GetRepositoryProjectId(gitProvider, repoId string) (projectId string, err error) {
	queryKey := []string{string(methods.RepositoryPathVariable), gitProvider, repoId}
	resp, err := s.Structure.tns.Lookup(tns.Query{Prefix: queryKey})
	if err != nil {
		err = fmt.Errorf("Lookup repository `%s` failed with: %s", repoId, err)
		return
	}

	respArr, ok := resp.([]string)
	if !ok || len(respArr) == 0 {
		err = fmt.Errorf("Response from lookup repository `%s` not an array or empty: `%v`", repoId, resp)
		return
	}

	for _, key := range respArr {
		if strings.Contains(key, "/type") {
			repoInfo := strings.Split(key, "/")
			if len(repoInfo) < 3 {
				err = fmt.Errorf("Invalid project key when getting /type for repository `%s`: %s", repoId, key)
				return
			}
			projectId = repoInfo[len(repoInfo)-2]
		}
	}

	return
}
