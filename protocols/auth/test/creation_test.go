package test

import (
	"fmt"
	"os"
	"testing"

	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	_ "github.com/taubyte/odo/protocols/auth"

	commonTest "github.com/taubyte/dreamland/helpers"
	gitTest "github.com/taubyte/dreamland/helpers/git"
	_ "github.com/taubyte/odo/clients/p2p/monkey"
	_ "github.com/taubyte/odo/clients/p2p/tns"
	protocolCommon "github.com/taubyte/odo/protocols/common"
	_ "github.com/taubyte/odo/protocols/hoarder"
	_ "github.com/taubyte/odo/protocols/tns"
)

func TestAuth(t *testing.T) {
	t.Skip("Need to be reimplemented")
	u := dreamland.Multiverse("test-config-job")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder": {},
			"tns":     {},
			"auth":    {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	protocolCommon.GetNewProjectID = func(args ...interface{}) string {
		return commonTest.ProjectID
	}

	authHttpPort, err := u.GetPortHttp(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}

	authHttpURL := fmt.Sprintf("http://127.0.0.1:%d", authHttpPort)
	err = commonTest.RegisterTestProject(u.Context(), authHttpURL)
	if err != nil {
		t.Error(err)
		return
	}

	gitRoot := "./testGIT"
	gitRootConfig := gitRoot + "/config"
	os.MkdirAll(gitRootConfig, 0755)
	defer os.RemoveAll(gitRootConfig)

	// clone repo
	err = gitTest.CloneToDirSSH(u.Context(), gitRootConfig, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	// TODO: Test with seer
}
