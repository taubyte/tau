package compile_test

import (
	"os"
	"path"
	"strings"
	"testing"

	"github.com/taubyte/config-compiler/decompile"
	_ "github.com/taubyte/config-compiler/fixtures"
	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	structureSpec "github.com/taubyte/go-specs/structure"
	_ "github.com/taubyte/odo/clients/p2p/tns"
	"github.com/taubyte/odo/protocols/monkey/fixtures/compile"
	_ "github.com/taubyte/odo/protocols/substrate"
	_ "github.com/taubyte/odo/protocols/tns"
)

func TestZipWebsite(t *testing.T) {
	dreamland.BigBang()
	u := dreamland.MultiverseWithConfig(dreamland.UniverseConfig{
		Name: "MonkeyFixtureTestWebsite",
		Id:   "MonkeyFixtureTestWebsite",
	})
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"node":    {},
			"hoarder": {},
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

	project, err := decompile.MockBuild(testProjectId, "",
		&structureSpec.Website{
			Id:      testWebsiteId,
			Name:    "someWebsite",
			Domains: []string{"someDomain"},
			Paths:   []string{"/"},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: "hal.computers.com",
		},
	)
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("injectProject", project)
	if err != nil {
		t.Error(err)
		return
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("compileFor", compile.BasicCompileFor{
		ProjectId:  testProjectId,
		ResourceId: testWebsiteId,
		Paths:      []string{path.Join(wd, "assets", "website.zip")},
	})
	if err != nil {
		t.Error(err)
		return
	}

	body, err := callHal(u, "/")
	if err != nil {
		t.Error(err)
		return
	}

	expectedToContain := "<title>Welcome</title>"
	if strings.Contains(string(body), expectedToContain) == false {
		t.Errorf("Expected %s to be in %s", expectedToContain, string(body))
		return
	}
}
