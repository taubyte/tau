package fixtures

import (
	"os"
	"testing"

	"github.com/taubyte/config-compiler/compile"
	commonIface "github.com/taubyte/go-interfaces/common"
	projectLib "github.com/taubyte/go-project-schema/project"
	functionSpec "github.com/taubyte/go-specs/function"
	websiteSpec "github.com/taubyte/go-specs/website"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	gitTest "github.com/taubyte/tau/libdream/helpers/git"
	dreamland "github.com/taubyte/tau/libdream/services"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestHttp(t *testing.T) {
	t.Skip("using an old project")
	u := dreamland.Multiverse("scratch")
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"me": {
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

	simple, err := u.Simple("me")
	if err != nil {
		t.Error(err)
		return
	}
	tns := simple.TNS()

	gitRoot := "./testGIT"
	gitRootConfig := gitRoot + "/prodConfig"
	os.MkdirAll(gitRootConfig, 0755)

	fakeMeta.Repository.SSHURL = "git@github.com:taubyte-test/tb_prodproject.git"
	fakeMeta.Repository.Provider = "github"

	err = gitTest.CloneToDirSSH(u.Context(), gitRootConfig, commonTest.Repository{
		ID:       517160737,
		Name:     "tb_prodproject",
		HookInfo: fakeMeta,
	})
	if err != nil {
		t.Error(err)
		return
	}

	// read with seer
	projectIface, err := projectLib.Open(projectLib.SystemFS(gitRootConfig))
	if err != nil {
		t.Error(err)
		return
	}

	rc, err := compile.CompilerConfig(projectIface, fakeMeta)
	if err != nil {
		t.Error(err)
		return
	}

	compiler, err := compile.New(rc, compile.Dev())
	if err != nil {
		t.Error(err)
		return
	}
	if err != nil {
		t.Error(err)
		return
	}
	defer compiler.Close()

	err = compiler.Build()
	if err != nil {
		t.Error(err)
		return
	}

	err = compiler.Publish(tns)
	if err != nil {
		t.Error(err)
		return
	}

	_path, err := websiteSpec.Tns().HttpPath("skelouse.com")
	if err != nil {
		t.Error(err)
		return
	}

	links := _path.Versioning().Links()
	test_obj, err := tns.Fetch(links)
	if test_obj == nil {
		t.Error("NO Object found", err)
		return
	}

	_path, err = functionSpec.Tns().HttpPath("pong.tau.link")
	if err != nil {
		t.Error(err)
		return
	}

	links = _path.Versioning().Links()
	test_obj, err = tns.Fetch(links)
	if test_obj == nil {
		t.Error("NO OBject found", err)
		return
	}

	currentPaths, err := test_obj.Current(fakeMeta.Repository.Branch)
	if err != nil || len(currentPaths) < 1 {
		t.Error("No paths found", err)
		return
	}

	for _, path := range currentPaths {
		currentObj, err := tns.Fetch(path)
		if err != nil {
			t.Error(err)
			return
		}

		if currentObj.Interface() == nil {
			t.Error("expected non nil object")
			return
		}
	}
}
