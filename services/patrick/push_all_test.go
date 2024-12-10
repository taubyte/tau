package service

import (
	"os"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/helpers"
	"gotest.tools/v3/assert"
)

func testRepoToken(t *testing.T) (tkn string) {
	if tkn = os.Getenv("TEST_GIT_TOKEN"); tkn == "" {
		t.SkipNow()
	}
	return
}

func TestPushAll(t *testing.T) {
	helpers.GitToken = testRepoToken(t)

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {},
			"monkey":  {},
			"hoarder": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:  &commonIface.ClientConfig{},
					Auth: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	mockAuthURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}

	err = helpers.RegisterTestRepositories(u.Context(), mockAuthURL, helpers.ConfigRepo, helpers.CodeRepo, helpers.LibraryRepo)
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(5 * time.Second)

	err = u.RunFixture("pushAll")
	assert.NilError(t, err)
}
