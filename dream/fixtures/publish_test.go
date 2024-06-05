package fixtures

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	testFixtures "github.com/taubyte/tau/pkg/config-compiler/fixtures"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	specs "github.com/taubyte/tau/pkg/specs/methods"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/tns"
)

func TestUpdate(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
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
