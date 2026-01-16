package fixtures

import (
	"context"
	"os"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

func TestHttp(t *testing.T) {
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
	if err != nil {
		t.Error(err)
		return
	}
	tns, err := simple.TNS()
	assert.NilError(t, err)

	gitRoot := "./testGIT"
	gitRootConfig := gitRoot + "/prodConfig"
	os.MkdirAll(gitRootConfig, 0755)

	fakeMeta.Repository.SSHURL = "git@github.com:taubyte-test/tb_prodproject.git"
	fakeMeta.Repository.Provider = "github"

	err = gitTest.CloneToDir(u.Context(), gitRootConfig, commonTest.Repository{
		ID:       517160737,
		Name:     "tb_prodproject",
		HookInfo: fakeMeta,
	})
	if err != nil {
		t.Error(err)
		return
	}

	// Create TCC compiler
	compiler, err := tccCompiler.New(
		tccCompiler.WithLocal(gitRootConfig),
		tccCompiler.WithBranch(fakeMeta.Repository.Branch),
	)
	if err != nil {
		t.Error(err)
		return
	}

	// Compile
	obj, validations, err := compiler.Compile(context.Background())
	if err != nil {
		t.Error(err)
		return
	}

	// Extract project ID from validations
	projectID, err := tcc.ExtractProjectID(validations)
	if err != nil {
		t.Error(err)
		return
	}

	// Process DNS validations (dev mode)
	err = tcc.ProcessDNSValidations(
		validations,
		generatedDomainRegExp,
		true, // dev mode
		nil,  // no DV key needed in dev mode
	)
	if err != nil {
		t.Error(err)
		return
	}

	// Extract object and indexes from Flat()
	flat := obj.Flat()
	object, ok := flat["object"].(map[string]interface{})
	if !ok {
		t.Error("object not found in flat result")
		return
	}

	indexes, ok := flat["indexes"].(map[string]interface{})
	if !ok {
		t.Error("indexes not found in flat result")
		return
	}

	// Publish to TNS
	err = tcc.Publish(
		tns,
		object,
		indexes,
		projectID,
		fakeMeta.Repository.Branch,
		fakeMeta.HeadCommit.ID,
	)
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

	currentPaths, err := test_obj.Current([]string{fakeMeta.Repository.Branch})
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
