package service

import (
	"testing"

	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonTest "github.com/taubyte/dreamland/helpers"
	commonIface "github.com/taubyte/go-interfaces/common"
)

func TestPushAll(t *testing.T) {
	u := dreamland.Multiverse("testPatrick")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {},
			"monkey":  {},
			"hoarder": {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS:  &commonIface.ClientConfig{},
					Auth: &commonIface.ClientConfig{},
				},
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

	err = commonTest.RegisterTestRepositories(u.Context(), mockAuthURL, commonTest.ConfigRepo, commonTest.CodeRepo, commonTest.LibraryRepo)
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("pushAll")
	if err != nil {
		t.Error(err)
		return
	}
}
