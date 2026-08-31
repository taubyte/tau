package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/auth/hooks"
	"github.com/taubyte/tau/services/auth/projects"
	"github.com/taubyte/tau/services/auth/repositories"

	"github.com/taubyte/tau/utils/id"
)

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

// repoAccess reports whether the caller may act on a repository. Get already
// fetches it under the caller's own token and GitHub returns that caller's
// permissions with it, so this costs no extra API call. Read access is not
// enough: a public repository reads to everyone, which is what let an unrelated
// caller reach another project's repositories.
func repoAccess(client GitHubClient, key repokey.Key) bool {
	if client.Get(key) != nil {
		return false
	}

	repo := client.Cur()
	if repo == nil {
		return false
	}

	perms := repo.GetPermissions()
	return perms["admin"] || perms["maintain"] || perms["push"]
}

// authorizeProject refuses a caller who can write to neither of the project's
// linked repositories. That is the same scoping getGitHubUserProjects applies
// when it lists projects, so a project is reachable by id to exactly the people
// who already see it in their own list.
func (srv *AuthService) authorizeProject(client GitHubClient, project projects.Project) error {
	if srv.devMode {
		return nil
	}

	for _, spelling := range []string{project.Config(), project.Code()} {
		key, err := repokey.Parse("https://" + spelling)
		if err != nil {
			continue
		}
		if repoAccess(client, key) {
			return nil
		}
	}

	// Worded like a missing project on purpose. Telling the caller a project
	// exists but isn't theirs turns the route back into the id oracle this
	// check exists to close.
	return errors.New("project not found")
}

func (srv *AuthService) registerGitHubRepository(ctx context.Context, client GitHubClient, key repokey.Key) (*RepositoryRegistrationResponse, error) {
	// If client is nil (P2P calls), skip GitHub API verification
	if client != nil {
		if !repoAccess(client, key) {
			return nil, fmt.Errorf("no access to repository `%s`", key)
		}

		if err := srv.authorizeRepository(client); err != nil {
			return nil, err
		}
	}

	repoKey := "/repositories/" + key.String() + "/key"

	hook_id := id.Generate(repoKey)
	defaultHookName := "taubyte_push_hook"
	defaultGithubHookUrl := srv.webHookUrl + "/github/" + hook_id

	var (
		hook_githubid int64
		secret        string
	)
	if !srv.devMode && client != nil {
		var err error
		hook_githubid, secret, err = client.CreatePushHook(&defaultHookName, &defaultGithubHookUrl, srv.devMode)
		if err != nil {
			return nil, fmt.Errorf("create push hook failed with: %s", err)
		}
	} else {
		// For P2P or dev mode, use mock values
		hook_githubid = 12345
		secret = "mock-secret"
	}

	kname, kpub, kpriv, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key failed with %s", err)
	}

	if client != nil {
		err = client.CreateDeployKey(&kname, &kpub)
		if err != nil {
			return nil, fmt.Errorf("create deploy key failed with: %s", err)
		}
	}

	repo := repositories.New(srv.KV(), key, kpriv, "")
	if err := repo.Register(ctx); err != nil {
		return nil, err
	}

	hook := hooks.NewGithub(srv.KV(), hook_id, int(hook_githubid), secret, key)
	if err := hook.Register(ctx); err != nil {
		return nil, fmt.Errorf("hooks register failed with %s", err)
	}

	repoInfo := make(map[string]string, 2)
	if client != nil {
		_repo, err := client.GetCurrentRepository()
		if err != nil {
			return nil, fmt.Errorf("get current repository failed with: %s", err)
		}

		if _repo.SSHURL != nil {
			repoInfo["ssh"] = *_repo.SSHURL
		}

		if _repo.FullName != nil {
			repoInfo["fullname"] = *_repo.FullName
		}
	} else {
		// this is p2p, maybe repository is public, try to get info using a client
		if client, err := srv.newGitHubClient(ctx, ""); err == nil {
			if err = client.Get(key); err == nil {
				if _repo, err := client.GetCurrentRepository(); err != nil {
					if _repo.SSHURL != nil {
						repoInfo["ssh"] = *_repo.SSHURL
					}
					if _repo.FullName != nil {
						repoInfo["fullname"] = *_repo.FullName
					}
				}
			}
		}
	}

	// The index is keyed by the segments of the canonical key, not by one
	// segment holding slashes: a TNS path segment never contains "/". The
	// literal "github" that used to sit here is gone with the numeric id it
	// disambiguated — the host is the first key segment, and it is the provider.
	if err := srv.tnsClient.Push(append([]string{"resolve", "repo"}, strings.Split(key.String(), "/")...), repoInfo); err != nil {
		return nil, fmt.Errorf("failed registering new job repo %s into tns with error: %v", key, err)
	}

	return &RepositoryRegistrationResponse{
		Key: repoKey,
	}, nil
}

func (srv *AuthService) unregisterGitHubRepository(ctx context.Context, client GitHubClient, key repokey.Key) error {
	// If client is nil (P2P calls), skip GitHub API verification
	if client != nil {
		// Registering already required write access and tenancy ownership;
		// unregistering tears down the deploy key and hooks, so it requires
		// the same. Read access alone would let anyone drop a public
		// repository's build wiring.
		if !repoAccess(client, key) {
			return fmt.Errorf("no access to repository `%s`", key)
		}

		if err := srv.authorizeRepository(client); err != nil {
			return err
		}
	}

	repo, err := repositories.Fetch(ctx, srv.KV(), key)
	if err != nil {
		return err
	}

	// Delete all hooks associated with the repository
	for _, hook := range repo.Hooks(ctx) {
		hook.Delete(ctx)
	}

	// Delete the repository
	return repo.Delete(ctx)
}

func (srv *AuthService) newGitHubProject(ctx context.Context, client GitHubClient, projectID, projectName string, config, code repokey.Key) (*ProjectCreateResponse, error) {
	logger.Debug("Creating project " + projectName)

	logger.Debug("Project ID=" + projectID)

	if !srv.devMode {
		// The caller is claiming these two repositories for a project, so they
		// have to be able to write to both. Without this, any id could be
		// pointed at repositories the caller has nothing to do with.
		for _, key := range []repokey.Key{config, code} {
			if !repoAccess(client, key) {
				return nil, fmt.Errorf("no access to repository `%s`", key)
			}
		}

		// Import supplies the project id, so this call can land on a project
		// that already exists and would otherwise overwrite it — including its
		// repository links — and add the caller as an owner.
		if existing, err := projects.Fetch(ctx, srv.KV(), projectID); err == nil {
			if err := srv.authorizeProject(client, existing); err != nil {
				return nil, err
			}
		}
	}

	gituser := client.Me()

	project, err := projects.New(srv.KV(), projects.Data{
		"id":       projectID,
		"name":     projectName,
		"provider": "github",
		"config":   config.String(),
		"code":     code.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create project object: %w", err)
	}

	err = project.Register()
	if err != nil {
		return nil, fmt.Errorf("failed to register project: %w", err)
	}

	err = srv.db.Put(ctx, "/projects/"+projectID+"/owners/"+fmt.Sprintf("%d", *(gituser.ID)), []byte(gituser.GetLogin()))
	if err != nil {
		return nil, err
	}

	for _, key := range []repokey.Key{config, code} {
		if err = srv.db.Put(ctx, "/repositories/"+key.String()+"/project", []byte(projectID)); err != nil {
			return nil, err
		}
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
	for id, info := range repos {
		key, err := repokey.Parse("https://" + id)
		if err != nil {
			continue
		}
		if repositories.Exist(ctx, srv.KV(), key) {
			user_repos[id] = RepositoryInfo{
				ID:   id,
				Name: info.Name,
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
		v, err := srv.db.Get(ctx, "/repositories/"+repo_id+"/project")
		if err == nil && len(v) > 0 {
			project_id := string(v)

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

	projects := make([]ProjectInfo, 0, len(user_projects))
	for _, project := range user_projects {
		projects = append(projects, project)
	}

	return &UserProjectsResponse{
		Projects: projects,
	}, nil
}

func (srv *AuthService) getGitHubProjectInfo(ctx context.Context, client GitHubClient, projectid string) (*ProjectInfoResponse, error) {
	project, err := projects.Fetch(ctx, srv.KV(), projectid)
	if err != nil {
		return nil, fmt.Errorf("retrieving project error: %w", err)
	}

	if err := srv.authorizeProject(client, project); err != nil {
		return nil, err
	}

	return &ProjectInfoResponse{
		Project: ProjectDetails{
			ID:   projectid,
			Name: project.Name(),
			Repositories: RepositoryDetails{
				Provider:      project.Provider(),
				Configuration: shortRepositoryInfo(client, project.Config()),
				Code:          shortRepositoryInfo(client, project.Code()),
			},
		},
	}, nil
}

func (srv *AuthService) deleteGitHubUserProject(ctx context.Context, client GitHubClient, projectid string) (*ProjectDeleteResponse, error) {
	project, err := projects.Fetch(ctx, srv.KV(), projectid)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch project: %w", err)
	}

	if err := srv.authorizeProject(client, project); err != nil {
		return nil, err
	}

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

// shortRepositoryInfo reports a stored repository key back to the caller,
// deriving it through the one grammar first so a row written by an older
// spelling is refused here rather than sent onward as if it were a key.
func shortRepositoryInfo(client GitHubClient, stored string) RepositoryShortInfo {
	key, err := repokey.Parse("https://" + stored)
	if err != nil {
		return RepositoryShortInfo{Error: err.Error()}
	}
	return client.ShortRepositoryInfo(key)
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
