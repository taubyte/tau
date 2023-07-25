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

	"github.com/taubyte/odo/protocols/auth/github"

	"github.com/taubyte/odo/protocols/auth/hooks"
	"github.com/taubyte/odo/protocols/auth/repositories"

	"github.com/taubyte/utils/id"

	corsjwt "bitbucket.org/taubyte/cors_jwt"
)

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

func (srv *AuthService) registerGitHubRepository(ctx context.Context, client *github.Client, repoID string) (map[string]interface{}, error) {
	err := client.GetByID(repoID)
	if err != nil {
		return nil, fmt.Errorf("fetch repository failed with %w", err)
	}

	repoKey := fmt.Sprintf("/repositories/github/%s/key", repoID)
	_, err = srv.db.Get(ctx, repoKey)
	if err == nil {
		return nil, fmt.Errorf("repository `%s` already registered", repoID)
	}

	hook_id := id.Generate(repoKey)
	defaultHookName := "taubyte_push_hook"
	var defaultGithubHookUrl string
	if srv.devMode {
		defaultGithubHookUrl = "https://hooks.git.taubyte.com/github/" + hook_id
	} else {
		defaultGithubHookUrl = srv.webHookUrl + "/github/" + hook_id
	}

	hook_githubid, secret, err := client.CreatePushHook(&defaultHookName, &defaultGithubHookUrl, srv.devMode)
	if err != nil {
		return nil, fmt.Errorf("create push hook failed with: %s", err)
	}

	kname, kpub, kpriv, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key failed with: %s", err)
	}

	err = client.CreateDeployKey(&kname, &kpub)
	if err != nil {
		return nil, fmt.Errorf("create deploy key failed with: %s", err)
	}

	_repo_id, err := strconv.ParseInt(repoID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse repoId failed with: %s", err)
	}

	repo, err := repositories.New(srv.KV(), repositories.Data{
		"id":       _repo_id,
		"provider": "github",
		"key":      kpriv,
	})
	if err != nil {
		return nil, fmt.Errorf("new repository failed with %s", err)
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
		return nil, fmt.Errorf("hooks new failed with: %s", err)
	}

	err = hook.Register(ctx)
	if err != nil {
		return nil, fmt.Errorf("hooks register failed with: %s", err)
	}

	_repo, err := client.GetCurrentRepository()
	if err != nil {
		return nil, fmt.Errorf("get current repository failed with: %s", err)
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
		return nil, fmt.Errorf("failed registering new job repo %d into tns with error: %v", _repo_id, err)
	}

	return map[string]interface{}{
		"key": repoKey,
	}, nil
}

func (srv *AuthService) unregisterGitHubRepository(ctx context.Context, client *github.Client, repoID string) (map[string]interface{}, error) {
	// select repo
	err := client.GetByID(repoID)
	if err != nil {
		return nil, fmt.Errorf("fetch repository failed with %w", err)
	}

	repoKey := fmt.Sprintf("/repositories/github/%s/key", repoID)
	kpriv, err := srv.db.Get(ctx, repoKey)
	if err != nil {
		return nil, fmt.Errorf("repository `%s` (%s) not registred! err = %w", repoID, repoKey, err)
	}

	_repo_id, err := strconv.ParseInt(repoID, 10, 64)
	if err != nil {
		return nil, err
	}

	repo, err := repositories.New(srv.KV(), repositories.Data{
		"id":       _repo_id,
		"provider": "github",
		"key":      string(kpriv),
	})
	if err != nil {
		return nil, err
	}

	for _, hook := range repo.Hooks(ctx) {
		// skip any error trying to delete the hook for now
		hook.Delete(ctx)
	}

	repo.Delete(ctx)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (srv *AuthService) newGitHubProject(ctx context.Context, client *github.Client, projectID, projectName, configID, codeID string) (map[string]interface{}, error) {
	response := make(map[string]interface{})
	logger.Debug("Creating project " + projectName)

	logger.Debug("Project ID=" + projectID)
	response["project"] = map[string]string{"id": projectID, "name": projectName}

	gituser := client.Me()
	project_key := "/projects/" + projectID
	err := srv.db.Put(ctx, project_key+"/name", []byte(projectName))
	if err != nil {
		return nil, err
	}

	if err = srv.db.Put(ctx, project_key+"/repositories/provider", []byte("github")); err != nil {
		return nil, err
	}

	err = srv.db.Put(ctx, project_key+"/owners/"+fmt.Sprintf("%d", *(gituser.ID)), []byte(gituser.GetLogin()))
	if err != nil {
		return nil, err
	}

	err = srv.db.Put(ctx, "/projects/"+projectID+"/repositories/config", []byte(configID))
	if err != nil {
		return nil, err
	}

	repo_key := fmt.Sprintf("/repositories/github/%s", configID)
	if err = srv.db.Put(ctx, repo_key+"/project", []byte(projectID)); err != nil {
		return nil, err
	}

	err = srv.db.Put(ctx, "/projects/"+projectID+"/repositories/code", []byte(codeID))
	if err != nil {
		return nil, err
	}

	repo_key = fmt.Sprintf("/repositories/github/%s", codeID)
	if err = srv.db.Put(ctx, repo_key+"/project", []byte(projectID)); err != nil {
		return nil, err
	}

	logger.Debugf("Project Add returned %v", response)

	return response, nil
}

func (srv *AuthService) getGitHubUserRepositories(ctx context.Context, client *github.Client) (map[string]interface{}, error) {
	response := make(map[string]interface{})
	repos := client.ListMyRepos()
	logger.Debugf("User repos:%v", repos)

	user_repos := make(map[string]interface{}, 0)
	for repo_id := range repos {
		repo_key := fmt.Sprintf("/repositories/github/%s/name", repo_id)
		v, err := srv.db.Get(ctx, repo_key)
		if err == nil && len(v) > 0 {
			logger.Debugf("Check %s got %s", repo_key, string(v))
			user_repos[repo_id] = map[string]interface{}{
				"id":   repo_id,
				"name": string(v),
			}
		}
	}

	logger.Debugf("getGitHubProjects: extracted %s", user_repos)

	response["repositories"] = user_repos

	return response, nil
}

func (srv *AuthService) getGitHubUserProjects(ctx context.Context, client *github.Client) (map[string]interface{}, error) {
	response := make(map[string]interface{})
	user_projects := make(map[string]interface{}, 0)
	for repo_id := range client.ListMyRepos() {
		repo_key := fmt.Sprintf("/repositories/github/%s/project", repo_id)
		v, err := srv.db.Get(ctx, repo_key)
		if err == nil && len(v) > 0 {
			project_id := string(v)
			proj_name_key := fmt.Sprintf("/projects/%s/name", project_id)
			proj_name, err := srv.db.Get(ctx, proj_name_key)
			if err == nil {
				if _, ok := user_projects[project_id]; !ok {
					user_projects[project_id] = map[string]interface{}{
						"id":   project_id,
						"name": string(proj_name),
					}
				}
			}
		}
	}

	logger.Debug(user_projects)

	response["projects"] = getMapValues(user_projects)

	return response, nil
}

func (srv *AuthService) getGitHubProjectInfo(ctx context.Context, client *github.Client, projectid string) (map[string]interface{}, error) {
	response := make(map[string]interface{})

	proj_prefix_key := fmt.Sprintf("/projects/%s/", projectid)

	proj_name, err := srv.db.Get(ctx, proj_prefix_key+"name")
	if err != nil {
		response["error"] = "Retreiving project error: " + err.Error()
		return response, nil
	}

	proj_gitprovider, err := srv.db.Get(ctx, proj_prefix_key+"repositories/provider")
	if err != nil {
		response["error"] = "Retreiving project error: " + err.Error()
		return response, nil
	}

	proj_config_repo, err := srv.db.Get(ctx, proj_prefix_key+"repositories/config")
	if err != nil {
		response["error"] = "Retreiving project error: " + err.Error()
		return response, nil
	}

	proj_code_repo, err := srv.db.Get(ctx, proj_prefix_key+"repositories/code")
	if err != nil {
		response["error"] = "Retreiving project error: " + err.Error()
		return response, nil
	}

	var jwtCorsToken string
	claims, err := corsjwt.New(corsjwt.GitHub(string(proj_config_repo)), corsjwt.GitHub(string(proj_code_repo)), corsjwt.Token(client.Token))
	if err == nil {
		jwtCorsToken, _ = claims.Sign()
	}

	response["project"] = map[string]interface{}{
		"id":   projectid,
		"name": string(proj_name),
		"repositories": map[string]interface{}{
			"provider":      string(proj_gitprovider),
			"configuration": client.ShortRepositoryInfo(string(proj_config_repo)),
			"code":          client.ShortRepositoryInfo(string(proj_code_repo)),
		},
		"cors": map[string]interface{}{
			"url":   "doci://__git_cors__.bridges.taubyte.com",
			"token": jwtCorsToken,
		},
	}

	return response, nil
}

func (srv *AuthService) deleteGitHubUserProject(ctx context.Context, client *github.Client, projectid string) (map[string]interface{}, error) {
	response := make(map[string]interface{})

	proj_prefix_key := fmt.Sprintf("/projects/%s/", projectid)

	c, err := srv.db.ListAsync(ctx, proj_prefix_key)
	if err != nil {
		response["error"] = "Retreiving project error: " + err.Error()
		return response, nil
	}

	for entry := range c {
		srv.db.Delete(ctx, entry)
	}

	response["project"] = map[string]interface{}{
		"id":     projectid,
		"status": "deleted",
	}

	return response, nil
}

func (srv *AuthService) getGitHubUser(client *github.Client) (map[string]interface{}, error) {
	response := make(map[string]interface{})

	gituser := client.Me()
	response["user"] = map[string]interface{}{
		"name":    gituser.GetName(),
		"company": gituser.GetCompany(),
		"email":   gituser.GetEmail(),
		"login":   gituser.GetLogin(),
	}

	return response, nil
}
