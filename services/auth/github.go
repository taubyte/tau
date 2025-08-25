package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strconv"

	"golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/services/auth/hooks"
	"github.com/taubyte/tau/services/auth/projects"
	"github.com/taubyte/tau/services/auth/repositories"

	"github.com/taubyte/tau/utils/id"
)

// Response structs for better type safety
type RepositoryRegistrationResponse struct {
	Key string `json:"key"`
}

type ProjectResponse struct {
	Project ProjectInfo `json:"project"`
}

type ProjectInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ProjectCreateResponse struct {
	Project ProjectInfo `json:"project"`
}

type RepositoryInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type UserRepositoriesResponse struct {
	Repositories map[string]RepositoryInfo `json:"repositories"`
}

type UserProjectsResponse struct {
	Projects []ProjectInfo `json:"projects"`
}

type RepositoryDetails struct {
	Provider      string              `json:"provider"`
	Configuration RepositoryShortInfo `json:"configuration"`
	Code          RepositoryShortInfo `json:"code"`
}

type ProjectInfoResponse struct {
	Project ProjectDetails `json:"project"`
}

type ProjectDetails struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Repositories RepositoryDetails `json:"repositories"`
}

type ProjectDeleteResponse struct {
	Project ProjectDeleteInfo `json:"project"`
}

type ProjectDeleteInfo struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type UserInfo struct {
	Name    string `json:"name"`
	Company string `json:"company"`
	Email   string `json:"email"`
	Login   string `json:"login"`
}

type UserResponse struct {
	User UserInfo `json:"user"`
}

// ref: https://github.com/keybase/bot-sshca/blob/master/src/keybaseca/sshutils/generate.go#L53
func generateKey() (string, string, string, error) {
	_privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", err
	}

	privateKey, err := x509.MarshalECPrivateKey(_privateKey)
	if err != nil {
		return "", "", "", err
	}

	privateKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey}
	var private bytes.Buffer
	if err := pem.Encode(&private, privateKeyPEM); err != nil {
		return "", "", "", err
	}

	pub, err := ssh.NewPublicKey(&_privateKey.PublicKey)
	if err != nil {
		return "", "", "", err
	}

	return deployKeyName, string(ssh.MarshalAuthorizedKey(pub)), private.String(), nil
}

func (srv *AuthService) registerGitHubRepository(ctx context.Context, client GitHubClient, repoID string) (*RepositoryRegistrationResponse, error) {
	err := client.GetByID(repoID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repository: %w", err)
	}

	repoKey := fmt.Sprintf("/repositories/github/%s/key", repoID)
	// Re-register a repo should be fine. Could be needed if deploy keys were deleted for example

	hook_id := id.Generate(repoKey)
	defaultHookName := "taubyte_push_hook"
	defaultGithubHookUrl := srv.webHookUrl + "/github/" + hook_id

	var (
		hook_githubid int64
		secret        string
	)
	if !srv.devMode {
		hook_githubid, secret, err = client.CreatePushHook(&defaultHookName, &defaultGithubHookUrl, srv.devMode)
		if err != nil {
			return nil, fmt.Errorf("failed to create push hook: %s", err)
		}
	}

	kname, kpub, kpriv, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %s", err)
	}

	// Dream also needs to create a deplyment key. TODO: use a pre-set key
	err = client.CreateDeployKey(&kname, &kpub)
	if err != nil {
		return nil, fmt.Errorf("failed to create deploy key: %s", err)
	}

	_repo_id, err := strconv.ParseInt(repoID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse repoId: %s", err)
	}

	repo, err := repositories.New(srv.KV(), repositories.Data{
		"id":       _repo_id,
		"provider": "github",
		"key":      kpriv,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create repository: %s", err)
	}

	err = repo.Register(ctx)
	if err != nil {
		return nil, err
	}

	hook, err := hooks.New(srv.KV(), hooks.Data{
		"id":         hook_id,
		"provider":   "github",
		"github_id":  hook_githubid,
		"repository": _repo_id,
		"secret":     secret,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create hook: %s", err)
	}

	err = hook.Register(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to register hook: %s", err)
	}

	_repo, err := client.GetCurrentRepository()
	if err != nil {
		return nil, fmt.Errorf("failed to get current repository: %s", err)
	}
	repoInfo := make(map[string]string, 0)

	if _repo.SSHURL != nil {
		repoInfo["ssh"] = *_repo.SSHURL
	}

	if _repo.FullName != nil {
		repoInfo["fullname"] = *_repo.FullName
	}
	//TODO add more items to the repoInfo that we are pushing to tns

	err = srv.tnsClient.Push([]string{"resolve", "repo", "github", fmt.Sprintf("%d", _repo_id)}, repoInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to register repo %d in tns: %v", _repo_id, err)
	}

	return &RepositoryRegistrationResponse{
		Key: repoKey,
	}, nil
}

func (srv *AuthService) unregisterGitHubRepository(ctx context.Context, client GitHubClient, repoID string) error {
	// select repo
	err := client.GetByID(repoID)
	if err != nil {
		return fmt.Errorf("failed to fetch repository: %w", err)
	}

	repoKey := fmt.Sprintf("/repositories/github/%s/key", repoID)
	kpriv, err := srv.db.Get(ctx, repoKey)
	if err != nil {
		return fmt.Errorf("repository `%s` (%s) not registered: %w", repoID, repoKey, err)
	}

	_repo_id, err := strconv.ParseInt(repoID, 10, 64)
	if err != nil {
		return err
	}

	repo, err := repositories.New(srv.KV(), repositories.Data{
		"id":       _repo_id,
		"provider": "github",
		"key":      string(kpriv),
	})
	if err != nil {
		return err
	}

	for _, hook := range repo.Hooks(ctx) {
		// skip any error trying to delete the hook for now
		hook.Delete(ctx)
	}

	repo.Delete(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (srv *AuthService) newGitHubProject(ctx context.Context, client GitHubClient, projectID, projectName, configID, codeID string) (*ProjectCreateResponse, error) {
	logger.Debug("Creating project " + projectName)

	logger.Debug("Project ID=" + projectID)

	gituser := client.Me()

	// Use projects helper instead of direct database manipulation
	project, err := projects.New(srv.KV(), projects.Data{
		"id":       projectID,
		"name":     projectName,
		"provider": "github",
		"config":   configID,
		"code":     codeID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}

	// Register the project
	err = project.Register()
	if err != nil {
		return nil, fmt.Errorf("failed to register project: %w", err)
	}

	// Add owner information
	err = srv.db.Put(ctx, "/projects/"+projectID+"/owners/"+fmt.Sprintf("%d", *(gituser.ID)), []byte(gituser.GetLogin()))
	if err != nil {
		return nil, err
	}

	// Link repositories to project
	repo_key := fmt.Sprintf("/repositories/github/%s", configID)
	if err = srv.db.Put(ctx, repo_key+"/project", []byte(projectID)); err != nil {
		return nil, err
	}

	repo_key = fmt.Sprintf("/repositories/github/%s", codeID)
	if err = srv.db.Put(ctx, repo_key+"/project", []byte(projectID)); err != nil {
		return nil, err
	}

	logger.Debugf("Project Add returned project ID=%s, name=%s", projectID, projectName)

	return &ProjectCreateResponse{
		Project: ProjectInfo{
			ID:   projectID,
			Name: projectName,
		},
	}, nil
}

func (srv *AuthService) getGitHubUserRepositories(ctx context.Context, client GitHubClient) (*UserRepositoriesResponse, error) {
	repos := client.ListMyRepos()
	logger.Debugf("User repos:%v", repos)

	user_repos := make(map[string]RepositoryInfo, 0)
	for repo_id := range repos {
		// Use repositories helper to check if repository exists
		if repositories.Exist(ctx, srv.KV(), repo_id) {
			// Get repository name from GitHub API or use ID as fallback
			repo_name := repo_id // fallback to ID if name not available
			user_repos[repo_id] = RepositoryInfo{
				ID:   repo_id,
				Name: repo_name,
			}
		}
	}

	logger.Debugf("getGitHubProjects: extracted %s", user_repos)

	return &UserRepositoriesResponse{
		Repositories: user_repos,
	}, nil
}

func (srv *AuthService) getGitHubUserProjects(ctx context.Context, client GitHubClient) (*UserProjectsResponse, error) {
	user_projects := make(map[string]ProjectInfo, 0)
	for repo_id := range client.ListMyRepos() {
		repo_key := fmt.Sprintf("/repositories/github/%s/project", repo_id)
		v, err := srv.db.Get(ctx, repo_key)
		if err == nil && len(v) > 0 {
			project_id := string(v)

			// Use projects helper instead of direct database query
			project, err := projects.Fetch(ctx, srv.KV(), project_id)
			if err == nil {
				if _, ok := user_projects[project_id]; !ok {
					user_projects[project_id] = ProjectInfo{
						ID:   project_id,
						Name: project.Name(),
					}
				}
			}
		}
	}

	logger.Debug(user_projects)

	// Convert map values to slice
	projects := make([]ProjectInfo, 0, len(user_projects))
	for _, project := range user_projects {
		projects = append(projects, project)
	}

	return &UserProjectsResponse{
		Projects: projects,
	}, nil
}

func (srv *AuthService) getGitHubProjectInfo(ctx context.Context, client GitHubClient, projectid string) (*ProjectInfoResponse, error) {
	// Use projects helper instead of direct database queries
	project, err := projects.Fetch(ctx, srv.KV(), projectid)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve project: %w", err)
	}

	return &ProjectInfoResponse{
		Project: ProjectDetails{
			ID:   projectid,
			Name: project.Name(),
			Repositories: RepositoryDetails{
				Provider:      project.Provider(),
				Configuration: client.ShortRepositoryInfo(project.Config()),
				Code:          client.ShortRepositoryInfo(project.Code()),
			},
		},
	}, nil
}

func (srv *AuthService) deleteGitHubUserProject(ctx context.Context, client GitHubClient, projectid string) (*ProjectDeleteResponse, error) {
	// Use projects helper to fetch and delete the project
	project, err := projects.Fetch(ctx, srv.KV(), projectid)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch project: %w", err)
	}

	// Delete the project using the helper
	err = project.Delete()
	if err != nil {
		return nil, fmt.Errorf("failed to delete project: %w", err)
	}

	return &ProjectDeleteResponse{
		Project: ProjectDeleteInfo{
			ID:     projectid,
			Status: "deleted",
		},
	}, nil
}

func (srv *AuthService) getGitHubUser(client GitHubClient) (*UserResponse, error) {
	gituser := client.Me()
	return &UserResponse{
		User: UserInfo{
			Name:    gituser.GetName(),
			Company: gituser.GetCompany(),
			Email:   gituser.GetEmail(),
			Login:   gituser.GetLogin(),
		},
	}, nil
}
