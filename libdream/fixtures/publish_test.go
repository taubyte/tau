package fixtures

import (
	"testing"

	"github.com/taubyte/config-compiler/compile"
	testFixtures "github.com/taubyte/config-compiler/fixtures"
	commonIface "github.com/taubyte/go-interfaces/common"
	projectSchema "github.com/taubyte/go-project-schema/project"
	specs "github.com/taubyte/go-specs/methods"
	dreamland "github.com/taubyte/tau/libdream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/protocols/tns"
)

func TestUpdate(t *testing.T) {
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
	tns, err := simple.TNS()
	assert.NilError(t, err)

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

	rc, err := compile.CompilerConfig(project, fakeMeta, generatedDomainRegExp)
	if err != nil {
		t.Error(err)
		return
	}

	compiler, err := compile.New(rc, compile.Dev())
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

	_, err = tns.Fetch(specs.ProjectPrefix(project.Get().Id(), fakeMeta.Repository.Branch, fakeMeta.HeadCommit.ID))
	if err != nil {
		t.Error(err)
		return
	}

	// TODO: Need to reimplement this check

	// if !reflect.DeepEqual(new_obj.Interface(), cc.CreatedProjectObject)  {
	// 	maps.Display("", new_obj.Interface())
	// 	fmt.Print("\n\n\n\n\n\n")
	// 	maps.Display("", createdProjectObject)
	// 	t.Error("Objects not equal")
	// 	return
	// }
}
