package service

import (
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	dreamland "github.com/taubyte/tau/libdream/services"
)

func TestPushAll(t *testing.T) {
	u := dreamland.Multiverse(dreamland.UniverseConfig{Name: "testPatrick"})
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
