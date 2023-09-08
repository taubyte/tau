package auth_test

import (
	"strconv"
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	dreamland "github.com/taubyte/tau/libdream"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	_ "github.com/taubyte/tau/protocols/auth"
	"github.com/taubyte/tau/protocols/auth/hooks"
	"github.com/taubyte/tau/protocols/auth/repositories"
	_ "github.com/taubyte/tau/protocols/tns"
	"gotest.tools/v3/assert"

	"github.com/taubyte/utils/id"
)

func TestAuthClient(t *testing.T) {
	u := dreamland.NewUniverse(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth": {},
			"tns":  {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					Auth: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	mockAuthURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}

	err = commonTest.RegisterTestProject(u.Context(), mockAuthURL)
	if err != nil {
		t.Error(err)
		return
	}

	hkNil, err := simple.Auth().Hooks().Get("")
	assert.Assert(t, err != nil)
	if hkNil != nil {
		t.Error("Returned Hook for empty id: ", hkNil)
	}

	/***** INIT *****/
	repo, err := repositories.New(u.Auth().KV(), repositories.Data{
		"id":       1,
		"provider": "github",
		"name":     "fake/repo",
		"project":  "fake_project_uuid",
		"key":      "fake_key",
		"url":      "fake_url",
	})
	if err != nil {
		t.Error("Repo creation error: ", err)
		return
	}

	err = repo.Register(u.Context())
	if err != nil {
		t.Error("Repo registeration error: ", err)
		return
	}

	/***** HOOKS *****/

	hook_id := id.Generate()
	// now let's create a hook
	hk, err := hooks.New(u.Auth().KV(), hooks.Data{
		"id":         hook_id,
		"provider":   "github",
		"github_id":  1,
		"repository": 1,
		"secret":     "fake_secret",
	})
	if err != nil {
		t.Error("Hook creation error: ", err)
		return
	}

	err = hk.Register(u.Context())
	if err != nil {
		t.Error("Hook registeration error: ", err)
		return
	}

	hk0, err := simple.Auth().Hooks().Get(hook_id)
	if err != nil {
		t.Error("Get Hook error: ", err)
	}

	if hk0 == nil {
		t.Error("Can't get hook")
	}

	hids, err := simple.Auth().Hooks().List()
	if err != nil {
		t.Error(err)
		return
	}

	found := false
	for _, id := range hids {
		if id == hook_id {
			found = true
			break
		}
	}

	if found == false {
		t.Errorf("%s was not found in hook id list", hook_id)
		return
	}

	/***** REPOS *****/

	repo0, err := simple.Auth().Repositories().Github().Get(1)
	if err != nil {
		t.Error("Get Repository error: ", err)
	}

	if repo0 == nil {
		t.Error("Can't get Repository")
	}

	ids, err := simple.Auth().Repositories().Github().List()
	if err != nil {
		t.Error(err)
		return

	}

	testId, err := strconv.Atoi(ids[0])
	if err != nil {
		t.Error(err)
		return
	}

	if testId != repo0.Id() {
		t.Errorf("Id didnt match got %d expected %d", testId, repo0.Id())
		return
	}

	err = repo.Delete(u.Context())
	if err != nil {
		t.Error("Repo delete error: ", err)
		return
	}

	_, err = simple.Auth().Hooks().Get(hook_id)
	if err == nil {
		t.Error("Delete Repo did not delete hooks!")
		return
	}

	pids, err := simple.Auth().Projects().List()
	if err != nil {
		t.Error(err)
		return
	}

	if len(pids) != 1 {
		t.Error("Expected one project id to be registered")
		return
	}

}
