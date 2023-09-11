package fixtures

import (
	"testing"

	"github.com/taubyte/config-compiler/compile"
	testFixtures "github.com/taubyte/config-compiler/fixtures"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/tns"
	projectSchema "github.com/taubyte/go-project-schema/project"
	dreamland "github.com/taubyte/tau/libdream"
	maps "github.com/taubyte/utils/maps"
	"gotest.tools/v3/assert"
)

func TestIndexer(t *testing.T) {
	t.Skip("needs to be reimplemented")
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Compat(),
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

	fs, err := testFixtures.VirtualFSWithBuiltProject()
	if err != nil {
		t.Error(err)
		return
	}

	project, err := projectSchema.Open(projectSchema.VirtualFS(fs, "/test_project/config"))
	if err != nil {
		t.Error(err)
		return
	}

	rc, err := compile.CompilerConfig(project, fakeMeta)
	if err != nil {
		t.Error(err)
		return
	}

	compiler, err := compile.New(rc, compile.Dev())
	if err != nil {
		t.Error(err)
		return
	}

	err = compiler.Build()
	if err != nil {
		t.Error(err)
		return
	}

	tnsClient, err := simple.TNS()
	assert.NilError(t, err)

	err = compiler.Publish(tnsClient)
	if err != nil {
		t.Error(err)
		return
	}

	resp, err := tnsClient.Lookup(tns.Query{Prefix: []string{"domains"}, RegEx: false})
	if err != nil {
		t.Error(err)
		return
	}

	_map := make(map[string]interface{})
	_map["test"] = resp
	list, err := maps.StringArray(_map, "test")
	if err != nil {
		t.Error(err)
		return
	}

	if len(list) != 2 { // local/global domains index, project,branch
		t.Errorf("Expected 2 got %d", len(list))
	}
}
