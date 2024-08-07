package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/tau/core/services/patrick"
	ifaceTNS "github.com/taubyte/tau/core/services/tns"
)

func (srv *PatrickService) connectToProject(ctx context.Context, job *patrick.Job) error {
	projectID, err := srv.getProjectIDFromJob(job)
	if err != nil {
		return err
	}

	err = srv.db.Put(ctx, fmt.Sprintf("/by/project/%s/%s", projectID, job.Id), []byte{})
	if err != nil {
		return fmt.Errorf("failed putting job into project with error: %w", err)
	}

	return nil
}

func (srv *PatrickService) getProjectIDFromJob(job *patrick.Job) (projectID string, err error) {
	repo, _ := srv.authClient.Repositories().Github().Get(job.Meta.Repository.ID)

	if repo != nil {
		projectID = repo.Project()
	}

	if len(projectID) == 0 {
		repo := job.Meta.Repository
		queryKey := []string{"repositories", strings.ToLower(repo.Provider), fmt.Sprintf("%d", repo.ID)}

		var resp interface{}
		resp, err = srv.tnsClient.Lookup(ifaceTNS.Query{Prefix: queryKey, RegEx: false})
		if err != nil {
			return
		}

		respArr, ok := resp.([]string)
		if !ok || len(respArr) == 0 {
			err = fmt.Errorf("response from lookup not an array or is empty: `%v`", resp)
			return
		}

		for _, key := range respArr {
			repoInfo := strings.Split(key, "/")
			if len(repoInfo) <= 4 {
				continue
			}
			projectID = repoInfo[4]
			if len(projectID) != 0 {
				break
			}
		}
		if len(projectID) == 0 {
			err = fmt.Errorf("projectID not found in response from tns for repo %d, got response: %s", repo.ID, resp)
		}
	}

	return
}
