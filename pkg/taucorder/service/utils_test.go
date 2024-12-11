package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/taubyte/tau/dream"
	dreamApi "github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	"github.com/taubyte/tau/services/auth/hooks"
	"github.com/taubyte/tau/services/auth/repositories"
	"github.com/taubyte/utils/id"
	"gotest.tools/v3/assert"
)

var (
	testProjectId  = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"
	testFunctionId = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"
	testLibraryId  = "QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt"
	testWebsiteId  = "QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2"
	testDomainId   = "QmNxpVc6DnbR3MKuvb3xw8Jzb8pfJTSRWJEdBMsb8AXFEX"
)

func init() {
	dream.DreamlandApiListen = "localhost:4224" // diffrent port than the default
	dreamApi.BigBang()
}

type MockConfigResolver struct {
	LookupFunc func(id string) (config.Parser, error)
}

func (m *MockConfigResolver) Lookup(id string) (config.Parser, error) {
	if m.LookupFunc != nil {
		return m.LookupFunc(id)
	}
	return nil, errors.New("not implemented")
}

const test_valid_config_id = "valid_config_id"

func getMockService(ctx context.Context) (*Service, error) {
	mockResolver := &MockConfigResolver{}

	// Use VirtConfig for a valid configuration
	_, cnf := fixtures.VirtConfig()
	mockResolver.LookupFunc = func(id string) (config.Parser, error) {
		if id == test_valid_config_id {
			return cnf, nil
		}
		return nil, errors.New("config not found")
	}

	s := &Service{
		ctx:      ctx,
		handlers: make(map[string]http.Handler),
		nodes:    make(map[string]*instance),
		resolver: mockResolver,
	}

	return s, nil
}

func registerFakeRepoHelper(t *testing.T, u *dream.Universe, _repo_id int64, kpriv string, hook_githubid int64, secret string) string {
	authKV := u.Auth().KV()
	ctx := context.Background()

	repo, err := repositories.New(authKV, repositories.Data{
		"id":       _repo_id,
		"provider": "github",
		"key":      kpriv,
	})
	assert.NilError(t, err)

	assert.NilError(t, repo.Register(ctx))

	hook, err := hooks.New(authKV, hooks.Data{
		"id":         id.Generate("github", hook_githubid),
		"provider":   "github",
		"github_id":  hook_githubid,
		"repository": _repo_id,
		"secret":     secret,
	})
	assert.NilError(t, err)

	assert.NilError(t, hook.Register(ctx))

	return hook.ID()
}

func createFakeProject(t *testing.T, u *dream.Universe, projectName string, gituserId int, gituserName string, configRepoID, codeRepoID string) (projectID string) {
	authKV := u.Auth().KV()
	ctx := context.Background()

	projectID = id.Generate()

	project_key := "/projects/" + projectID
	assert.NilError(t, authKV.Put(ctx, project_key+"/name", []byte(projectName)))

	assert.NilError(t, authKV.Put(ctx, project_key+"/repositories/provider", []byte("github")))

	assert.NilError(t, authKV.Put(ctx, fmt.Sprintf("%s/owners/%d", project_key, gituserId), []byte(gituserName)))

	assert.NilError(t, authKV.Put(ctx, "/projects/"+projectID+"/repositories/config", []byte(configRepoID)))

	assert.NilError(t, authKV.Put(ctx, fmt.Sprintf("/repositories/github/%s/project", configRepoID), []byte(projectID)))

	assert.NilError(t, authKV.Put(ctx, "/projects/"+projectID+"/repositories/code", []byte(codeRepoID)))

	assert.NilError(t, authKV.Put(ctx, fmt.Sprintf("/repositories/github/%s/project", codeRepoID), []byte(projectID)))

	configRepoIDInt, err := strconv.ParseInt(configRepoID, 10, 64)
	assert.NilError(t, err)
	registerFakeRepoHelper(t, u, configRepoIDInt, "fake-deploy-priv-key", 101, "fake-hook-secret")

	codeRepoIDInt, err := strconv.ParseInt(codeRepoID, 10, 64)
	assert.NilError(t, err)
	registerFakeRepoHelper(t, u, codeRepoIDInt, "fake-deploy-priv-key", 101, "fake-hook-secret")

	return
}

func registerFakeRepo(t *testing.T, u *dream.Universe, repoID string) string {
	_repoID, err := strconv.ParseInt(repoID, 10, 64)
	assert.NilError(t, err)
	return registerFakeRepoHelper(t, u, _repoID, "fake-deploy-priv-key", 101, "fake-hook-secret")
}

func injectFakeStaticCert(t *testing.T, u *dream.Universe, domain string, cert []byte) {
	authKV := u.Auth().KV()
	ctx := context.Background()
	assert.NilError(t, authKV.Put(ctx, "/static/"+base64.StdEncoding.EncodeToString([]byte(domain))+"/certificate/pem", cert))
}

func injectFakeACMECert(t *testing.T, u *dream.Universe, domain string, cert []byte) {
	authKV := u.Auth().KV()
	ctx := context.Background()
	assert.NilError(t, authKV.Put(ctx, "/acme/"+base64.StdEncoding.EncodeToString([]byte(domain))+"/certificate/pem", cert))
}
