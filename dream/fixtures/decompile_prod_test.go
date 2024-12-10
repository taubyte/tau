package fixtures

import (
	"os"
	"testing"

	"github.com/spf13/afero"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	specs "github.com/taubyte/tau/pkg/specs/methods"
	_ "github.com/taubyte/tau/services/tns"
	"github.com/taubyte/utils/maps"
	"gotest.tools/v3/assert"
)

func TestDecompileProd(t *testing.T) {
	t.Skip("using an old project")
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"me": {
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

	simple, err := u.Simple("me")
	assert.NilError(t, err)

	tns, err := simple.TNS()
	assert.NilError(t, err)

	gitRoot := "./testGIT"
	gitRootConfig := gitRoot + "/prodConfigDreamland"
	os.MkdirAll(gitRootConfig, 0755)

	fakeMeta := commonTest.ConfigRepo.HookInfo
	fakeMeta.Repository.SSHURL = "git@github.com:taubyte-test/tb_prodproject.git"
	fakeMeta.Repository.Branch = "dreamland"
	fakeMeta.Repository.Provider = "github"

	err = gitTest.CloneToDirSSH(u.Context(), gitRootConfig, commonTest.Repository{
		ID:       517160737,
		Name:     "tb_prodproject",
		HookInfo: fakeMeta,
	})
	assert.NilError(t, err)

	// read with seer
	projectIface, err := projectLib.Open(projectLib.SystemFS(gitRootConfig))
	assert.NilError(t, err)

	rc, err := compile.CompilerConfig(projectIface, fakeMeta, generatedDomainRegExp)
	assert.NilError(t, err)

	compiler, err := compile.New(rc, compile.Dev())
	assert.NilError(t, err)

	err = compiler.Build()
	assert.NilError(t, err)

	err = compiler.Publish(tns)
	assert.NilError(t, err)

	test_obj, err := tns.Fetch(specs.ProjectPrefix(projectIface.Get().Id(), fakeMeta.Repository.Branch, fakeMeta.HeadCommit.ID))
	if test_obj.Interface() == nil {
		t.Error("NO OBject found", err)
		return
	}

	maps.Display("", test_obj)

	testProjectDir := "./testGIT/testDecompileProd"
	os.RemoveAll(testProjectDir)
	os.Mkdir(testProjectDir, 0777)

	decompiler, err := decompile.New(afero.NewBasePathFs(afero.NewOsFs(), testProjectDir), test_obj.Interface())
	if err != nil {
		t.Error(err)
		return
	}

	_, err = decompiler.Build()
	if err != nil {
		t.Error(err)
	}

}
