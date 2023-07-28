package fixtures

import (
	"testing"

	"github.com/taubyte/config-compiler/compile"
	testFixtures "github.com/taubyte/config-compiler/fixtures"
	commonIface "github.com/taubyte/go-interfaces/common"
	projectSchema "github.com/taubyte/go-project-schema/project"
	specs "github.com/taubyte/go-specs/methods"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	"github.com/taubyte/tau/libdream/services"

	_ "github.com/taubyte/tau/protocols/tns"
)

func TestUpdate(t *testing.T) {
	t.Skip("needs to be reimplemented")
	u := services.Multiverse("single_e2e")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
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

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}
	tns := simple.TNS()

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

	// if reflect.DeepEqual(new_obj.Interface(), cc.CreatedProjectObject) == false {
	// 	maps.Display("", new_obj.Interface())
	// 	fmt.Print("\n\n\n\n\n\n")
	// 	maps.Display("", createdProjectObject)
	// 	t.Error("Objects not equal")
	// 	return
	// }
}
