//go:build dreaming

package auth_test

import (
	"strconv"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	_ "github.com/taubyte/tau/services/auth/dream"
	"github.com/taubyte/tau/services/auth/hooks"
	"github.com/taubyte/tau/services/auth/repositories"
	_ "github.com/taubyte/tau/services/tns/dream"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/utils/id"
)

func TestAuthClient_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth": {},
			"tns":  {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Auth: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(5 * time.Second)

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	auth, err := simple.Auth()
	assert.NilError(t, err)

	err = commonTest.RegisterTestProject(u.Context(), auth)
	if err != nil {
		t.Error(err)
		return
	}

	hkNil, err := auth.Hooks().Get("")
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

	hk0, err := auth.Hooks().Get(hook_id)
	if err != nil {
		t.Error("Get Hook error: ", err)
	}

	if hk0 == nil {
		t.Error("Can't get hook")
	}

	hids, err := auth.Hooks().List()
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

	if !found {
		t.Errorf("%s was not found in hook id list", hook_id)
		return
	}

	/***** REPOS *****/

	repo0, err := auth.Repositories().Github().Get(1)
	if err != nil {
		t.Error("Get Repository error: ", err)
	}

	if repo0 == nil {
		t.Error("Can't get Repository")
	}

	ids, err := auth.Repositories().Github().List()
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

	_, err = auth.Hooks().Get(hook_id)
	if err == nil {
		t.Error("Delete Repo did not delete hooks!")
		return
	}

	pids, err := auth.Projects().List()
	if err != nil {
		t.Error(err)
		return
	}

	if len(pids) != 1 {
		t.Error("Expected one project id to be registered")
		return
	}

}
