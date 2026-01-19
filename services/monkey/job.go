package monkey

import (
	"fmt"
	"strings"

	authClient "github.com/taubyte/tau/clients/p2p/auth"
	"github.com/taubyte/tau/core/common"
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
	repoType := common.UnknownRepository

	gitRepo, err := m.tryGetGitRepo(ac, repo.ID)
	if err != nil {
		return fmt.Errorf("run job failed during fetching with %w", err)
	}

	projectId = gitRepo.Project()
	if projectId != "" {
		p = ac.Projects().Get(projectId)
		if p == nil {
			return fmt.Errorf("project not found: %s", projectId)
		}

		switch repo.ID {
		case p.Git.Code.Id():
			repoType = common.CodeRepository
		case p.Git.Config.Id():
			repoType = common.ConfigRepository
		}
	}

	repo.Provider = strings.ToLower(repo.Provider)

	if len(projectId) == 0 {
		projectId, err = m.Service.tnsClient.Simple().GetRepositoryProjectId(repo.Provider, repoID)
		if err != nil {
			return
		}
	}

	if repoType == common.UnknownRepository {
		p := ac.Projects().Get(projectId)
		if p == nil {
			return fmt.Errorf("project not found: %s", projectId)
		}

		_repoPathKey, err := methods.GetRepositoryPath(repo.Provider, repoID, projectId)
		if err != nil {
			return err
		}

		repoTypeResponse, err := m.Service.tnsClient.Fetch(_repoPathKey.Type())
		if err != nil {
			return fmt.Errorf("fetch project failed with: %w", err)
		}

		repoType = common.RepositoryType(toNumber(repoTypeResponse.Interface()))
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
	}

	c.Context(m.ctx)

	if repoType == common.CodeRepository {
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
