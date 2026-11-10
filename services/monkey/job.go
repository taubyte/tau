package monkey

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	authClient "github.com/taubyte/tau/clients/p2p/auth"
	"github.com/taubyte/tau/core/common/repositorytype"
	"github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/services/monkey/jobs"
)

func (m *Monkey) RunJob() (err error) {
	repo := m.Job.Meta.Repository
	repoID := fmt.Sprintf("%d", repo.ID)
	if repo.ID <= 1 {
		if repo.ID == 0 {
			return fmt.Errorf("no repository defined for job: %s", m.Job.Id)
		}

		return nil
	}

	node := m.Service.node
	if m.Service.clientNode != nil {
		node = m.Service.clientNode
	}

	ac, err := authClient.New(m.ctx, node)
	if err != nil {
		return fmt.Errorf("auth client new failed with: %w", err)
	}

	var projectId string
	var p *auth.Project
	repoType := repositorytype.UnknownRepository

	gitRepo, err := m.tryGetGitRepo(ac, repo.ID)
	if err != nil {
		return fmt.Errorf("run job failed during fetching with %w", err)
	}

	projectId = gitRepo.Project()
	if projectId != "" {
		if p, err = waitForProject(m.ctx, ac, projectId); err != nil {
			return err
		}

		switch repo.ID {
		case p.Git.Code.Id():
			repoType = repositorytype.CodeRepository
		case p.Git.Config.Id():
			repoType = repositorytype.ConfigRepository
		}
	}

	repo.Provider = strings.ToLower(repo.Provider)

	if len(projectId) == 0 {
		projectId, err = waitForProjectID(m.ctx, func() (string, error) {
			return m.Service.tnsClient.Simple().GetRepositoryProjectId(repo.Provider, repoID)
		})
		if err != nil {
			return
		}
	}

	if repoType == repositorytype.UnknownRepository {
		_, err := waitForProject(m.ctx, ac, projectId)
		if err != nil {
			return err
		}

		_repoPathKey, err := methods.GetRepositoryPath(repo.Provider, repoID, projectId)
		if err != nil {
			return err
		}

		repoTypeResponse, err := m.Service.tnsClient.Fetch(_repoPathKey.Type())
		if err != nil {
			return fmt.Errorf("fetch project failed with: %w", err)
		}

		repoType = repositorytype.Type(toNumber(repoTypeResponse.Interface()))
	}

	c := jobs.Context{
		Patrick:               m.Service.patrickClient,
		Monkey:                m.Service,
		Tns:                   m.Service.tnsClient,
		RepoType:              repoType,
		ProjectID:             projectId,
		DeployKey:             gitRepo.PrivateKey(),
		Job:                   m.Job,
		Node:                  m.Service.node,
		LogFile:               m.logFile,
		ClientNode:            node,
		DVPublicKey:           m.Service.dvPublicKey,
		GeneratedDomainRegExp: m.generatedDomainRegExp,
		NetworkFqdn:           m.Service.config.NetworkFqdn(),
	}

	c.Context(m.ctx)

	if repoType == repositorytype.CodeRepository {
		c.ConfigRepoId = p.Git.Config.Id()

		configRepo, err := ac.Repositories().Github().Get(p.Git.Config.Id())
		if err != nil {
			return fmt.Errorf("auth github get failed with: %w", err)
		}
		c.ConfigPrivateKey = configRepo.PrivateKey()
	}

	if err = c.Run(m.ctx); err != nil {
		return fmt.Errorf("running job for type: %d on repo: %d failed with: %s", repoType, m.Job.Meta.Repository.ID, err.Error())
	}

	return nil
}

// waitForProjectID retries a repository's project lookup for up to
// jobs.TnsRetryFor. A new repository's project is published by the config
// job; Patrick hands its job out once it sees the project, but this node's
// TNS can lag a moment behind, and failing then cost the job a try.
func waitForProjectID(ctx context.Context, lookup func() (string, error)) (string, error) {
	deadline := time.Now().Add(jobs.TnsRetryFor)
	for {
		id, err := lookup()
		if err == nil && id == "" {
			err = errors.New("repository has no project yet")
		}
		if err == nil || time.Now().After(deadline) {
			return id, err
		}
		select {
		case <-ctx.Done():
			return "", err
		case <-time.After(time.Second):
		}
	}
}

// waitForProject reads a project from auth, waiting up to jobs.TnsRetryFor:
// a new repository's job can reach this node before the project it belongs
// to is readable here, and failing then failed the build for good.
func waitForProject(ctx context.Context, ac projectGetter, projectId string) (*auth.Project, error) {
	var p *auth.Project
	_, err := waitForProjectID(ctx, func() (string, error) {
		if p = ac.Projects().Get(projectId); p == nil {
			return "", fmt.Errorf("project not found: %s", projectId)
		}
		return projectId, nil
	})
	return p, err
}

type projectGetter interface {
	Projects() auth.Projects
}
