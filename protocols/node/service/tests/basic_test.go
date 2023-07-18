package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"testing"

	"github.com/taubyte/config-compiler/decompile"
	dreamlandCommon "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/monkey/fixtures/compile"

	_ "github.com/taubyte/config-compiler/fixtures"
	_ "github.com/taubyte/odo/protocols/auth/service"
	_ "github.com/taubyte/odo/protocols/hoarder/service"
	_ "github.com/taubyte/odo/protocols/node/service"
	_ "github.com/taubyte/odo/protocols/patrick/service"
	_ "github.com/taubyte/odo/protocols/tns/service"
)

var (
	testProjectId  = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"
	testFunctionId = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"
	testLibraryId  = "QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt"
	testWebsiteId  = "QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2"
)

func TestBasicWithLibrary(t *testing.T) {
	u := dreamland.Multiverse("TestBasicWithLibrary")
	defer u.Stop()

	err := u.StartWithConfig(&dreamlandCommon.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder": {},
			"tns":     {},
			"node":    {},
			"auth":    {},
			"patrick": {},
			"monkey":  {},
		},
		Simples: map[string]dreamlandCommon.SimpleConfig{
			"client": {
				Clients: dreamlandCommon.SimpleConfigClients{
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

	// TODO: This revisit this part
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
	// if strings.Contains(string(body), expectedToContain) == false {
	// 	t.Errorf("Expected %s to be in %s", expectedToContain, string(body))
	// 	return
	// }
}

func callHal(u dreamlandCommon.Universe, path string) ([]byte, error) {
	if u == nil {
		return nil, errors.New("universe nil")
	}
	nodePort, err := u.GetPortHttp(u.Node().Node())
	if err != nil {
		return nil, err
	}

	host := fmt.Sprintf("hal.computers.com:%d", nodePort)
	ret, err := http.DefaultClient.Get(fmt.Sprintf("http://%s%s", host, path))
	if err != nil {
		return nil, err
	}
	defer ret.Body.Close()

	return ioutil.ReadAll(ret.Body)
}
