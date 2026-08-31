package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	"github.com/taubyte/tau/services/auth/hooks"
	"github.com/taubyte/tau/services/auth/repositories"
	"github.com/taubyte/tau/utils/id"
	"gotest.tools/v3/assert"
)

var (
	testProjectId  = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"
	testFunctionId = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"
	testLibraryId  = "QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt"
	testWebsiteId  = "QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2"
	testDomainId   = "QmNxpVc6DnbR3MKuvb3xw8Jzb8pfJTSRWJEdBMsb8AXFEX"
)

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

func registerFakeRepoHelper(t *testing.T, u *dream.Universe, repoID string, kpriv string, hook_githubid int64, secret string) string {
	authKV := u.Auth().KV()
	ctx := context.Background()

	key, err := repokey.Parse("https://" + repoID)
	assert.NilError(t, err)

	assert.NilError(t, repositories.New(authKV, key, kpriv, "").Register(ctx))

	hook := hooks.NewGithub(authKV, id.Generate("github", hook_githubid), int(hook_githubid), secret, key)
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

	assert.NilError(t, authKV.Put(ctx, "/repositories/"+configRepoID+"/project", []byte(projectID)))

	assert.NilError(t, authKV.Put(ctx, "/projects/"+projectID+"/repositories/code", []byte(codeRepoID)))

	assert.NilError(t, authKV.Put(ctx, "/repositories/"+codeRepoID+"/project", []byte(projectID)))

	registerFakeRepoHelper(t, u, configRepoID, "fake-deploy-priv-key", 101, "fake-hook-secret")
	registerFakeRepoHelper(t, u, codeRepoID, "fake-deploy-priv-key", 101, "fake-hook-secret")

	return
}

func registerFakeRepo(t *testing.T, u *dream.Universe, repoID string) string {
	return registerFakeRepoHelper(t, u, repoID, "fake-deploy-priv-key", 101, "fake-hook-secret")
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
