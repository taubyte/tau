package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/monkey/fixtures/compile"

	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/pkg/config-compiler/fixtures"
	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/patrick"
	_ "github.com/taubyte/tau/services/substrate"
	_ "github.com/taubyte/tau/services/tns"
)

var (
	testProjectId  = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"
	testFunctionId = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"
	testLibraryId  = "QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt"
	testWebsiteId  = "QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2"
)

func TestBasicWithLibrary(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder":   {},
			"tns":       {},
			"substrate": {},
			"auth":      {},
			"patrick":   {},
			"monkey":    {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	project, err := decompile.MockBuild(testProjectId, "",
		&structureSpec.Library{
			Id:   testLibraryId,
			Name: "someLibrary",
			Path: "/",
		},
		&structureSpec.Function{
			Id:      testFunctionId,
			Name:    "someFunc",
			Type:    "http",
			Call:    "ping",
			Memory:  100000,
			Timeout: 1000000000,
			Method:  "GET",
			Source:  "libraries/someLibrary",
			Domains: []string{"someDomain"},
			Paths:   []string{"/ping"},
		},
		&structureSpec.Domain{
			Name: "someDomain",
			Fqdn: "hal.computers.com",
		},
		&structureSpec.Website{
			Id:      testWebsiteId,
			Name:    "someWebsite",
			Domains: []string{"someDomain"},
			Paths:   []string{"/"},
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
		ResourceId: testLibraryId,
		Paths:      []string{path.Join(wd, "_assets", "library")},

		// Uncomment and change directory to use cached build
		// Path: "/tmp/QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt-556050950/artifact.zwasm",
	})
	if err != nil {
		t.Error(err)
		return
	}

	body, err := callHal(u, "/ping")
	if err != nil {
		t.Error(err)
		return
	}

	if string(body) != "PONG" {
		t.Error("Expected PONG got", string(body))
		return
	}

	// TODO: This revisit Website Compile Fixture, Keep commented code
	// err = u.RunFixture("compileFor", compile.BasicCompileFor{
	// 	ProjectId:  testProjectId,
	// 	ResourceId: testWebsiteId,
	// 	Paths:      []string{path.Join(wd, "_assets", "website")},

	// 	// Uncomment and change directory to use cached build
	// 	// Path: "/tmp/QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2-3889885873/build.zip",
	// })
	// if err != nil {
	// 	t.Error("here", err)
	// 	return
	// }

	// body, err = callHal(u, "/")
	// if err != nil {
	// 	t.Error(err)
	// 	return
	// }

	// expectedToContain := "<title>Welcome</title>"
	// if !strings.Contains(string(body), expectedToContain)  {
	// 	t.Errorf("Expected %s to be in %s", expectedToContain, string(body))
	// 	return
	// }
}

func callHal(u *dream.Universe, path string) ([]byte, error) {
	if u == nil {
		return nil, errors.New("universe nil")
	}
	nodePort, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		return nil, err
	}

	host := fmt.Sprintf("hal.computers.com:%d", nodePort)
	ret, err := http.DefaultClient.Get(fmt.Sprintf("http://%s%s", host, path))
	if err != nil {
		return nil, err
	}
	defer ret.Body.Close()

	return io.ReadAll(ret.Body)
}
