package fixtures

import (
	"context"
	"os"
	"testing"

	"github.com/spf13/afero"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	specs "github.com/taubyte/tau/pkg/specs/methods"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	_ "github.com/taubyte/tau/services/tns/dream"
	"github.com/taubyte/tau/utils/maps"
	"github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

func TestDecompileProd(t *testing.T) {
	t.Skip("using an old project")

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
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
	gitRootConfig := gitRoot + "/prodConfigDream"
	os.MkdirAll(gitRootConfig, 0755)

	fakeMeta := commonTest.ConfigRepo.HookInfo
	fakeMeta.Repository.SSHURL = "git@github.com:taubyte-test/tb_prodproject.git"
	fakeMeta.Repository.Branch = "dream"
	fakeMeta.Repository.Provider = "github"

	err = gitTest.CloneToDir(u.Context(), gitRootConfig, commonTest.Repository{
		ID:       517160737,
		Name:     "tb_prodproject",
		HookInfo: fakeMeta,
	})
	assert.NilError(t, err)

	// Create TCC compiler
	compiler, err := tccCompiler.New(
		tccCompiler.WithLocal(gitRootConfig),
		tccCompiler.WithBranch(fakeMeta.Repository.Branch),
	)
	assert.NilError(t, err)

	// Compile
	obj, validations, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	// Extract project ID from validations
	projectID, err := tcc.ExtractProjectID(validations)
	assert.NilError(t, err)

	// Process DNS validations (dev mode)
	err = tcc.ProcessDNSValidations(
		validations,
		generatedDomainRegExp,
		true, // dev mode
		nil,  // no DV key needed in dev mode
	)
	assert.NilError(t, err)

	// Extract object and indexes from Flat()
	flat := obj.Flat()
	object, ok := flat["object"].(map[string]interface{})
	assert.Assert(t, ok, "object not found in flat result")

	indexes, ok := flat["indexes"].(map[string]interface{})
	assert.Assert(t, ok, "indexes not found in flat result")

	// Publish to TNS
	err = tcc.Publish(
		tns,
		object,
		indexes,
		projectID,
		fakeMeta.Repository.Branch,
		fakeMeta.HeadCommit.ID,
	)
	assert.NilError(t, err)

	test_obj, err := tns.Fetch(specs.ProjectPrefix(projectID, fakeMeta.Repository.Branch, fakeMeta.HeadCommit.ID))
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
