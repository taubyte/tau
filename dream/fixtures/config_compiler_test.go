package fixtures

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	"gotest.tools/v3/assert"

	commonIface "github.com/taubyte/tau/core/common"

	"github.com/spf13/afero"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
	specs "github.com/taubyte/tau/pkg/specs/methods"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	"github.com/taubyte/tau/utils/tcc"
)

func TestE2E(t *testing.T) {
	t.Skip("Needs to be redone")

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
	gitRootConfig := gitRoot + "/config"
	os.MkdirAll(gitRootConfig, 0755)
	fakeMeta.Repository.Provider = "github"

	err = gitTest.CloneToDir(u.Context(), gitRootConfig, commonTest.ConfigRepo)
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

	// Get project interface for later use
	projectIface, err := projectLib.Open(projectLib.SystemFS(gitRootConfig))
	if err != nil {
		t.Error(err)
		return
	}

	_path, err := websiteSpec.Tns().HttpPath("testing_website_builder.com")
	if err != nil {
		t.Error(err)
		return
	}

	links := _path.Versioning().Links()
	test_obj, err := tns.Fetch(links)
	if test_obj == nil {
		t.Error("NO OBject found", err)
		return
	}

	_, globalFunctions := projectIface.Get().Functions("")
	for _, function := range globalFunctions {
		wasmPath, err := functionSpec.Tns().WasmModulePath(
			projectID,
			"",
			function,
		)
		if err != nil {
			t.Error(err)
			return
		}

		test_obj, err = tns.Fetch(wasmPath)
		if err != nil || test_obj == nil {
			t.Error("NO OBject found", err)
			return
		}
	}

	_, globalLibraries := projectIface.Get().Libraries("")
	for _, library := range globalLibraries {
		wasmPath, err := librarySpec.Tns().WasmModulePath(
			projectID,
			"",
			library,
		)
		if err != nil {
			t.Error(err)
			return
		}

		test_obj, err = tns.Fetch(wasmPath)
		if err != nil || test_obj == nil {
			t.Error("NO OBject found", err)
			return
		}
	}

	// fetch
	new_obj, err := tns.Fetch(
		specs.ProjectPrefix(
			projectID,
			fakeMeta.Repository.Branch,
			fakeMeta.HeadCommit.ID,
		),
	)
	if err != nil {
		t.Error(err)
		return
	}
	if new_obj == nil {
		t.Error("NO OBJECT FETCHED")
		return
	}

	// expect keys
	_, err = tns.Lookup(tnsIface.Query{Prefix: []string{"repositories"}, RegEx: false})
	if err != nil {
		t.Errorf("fetch keys failed with err: %s", err.Error())
		return
	}

	// decompile
	gitRootConfig_new := gitRootConfig + "_new"
	os.MkdirAll(gitRootConfig_new, 0755)
	decompiler, err := decompile.New(afero.NewBasePathFs(afero.NewOsFs(), gitRootConfig_new), new_obj.Interface())
	if err != nil {
		t.Error(err)
		return
	}

	_, err = decompiler.Build()
	if err != nil {
		t.Error(err)
		return
	}

	// check diff
	// compare gitRootConfig and gitRootConfig_new
	// Compile original project
	compiler1, err := tccCompiler.New(
		tccCompiler.WithLocal(gitRootConfig),
		tccCompiler.WithBranch(fakeMeta.Repository.Branch),
	)
	if err != nil {
		t.Error(err)
		return
	}

	obj1, _, err := compiler1.Compile(context.Background())
	if err != nil {
		t.Error(err)
		return
	}

	flat1 := obj1.Flat()
	_map, ok := flat1["object"].(map[string]interface{})
	if !ok {
		t.Error("object not found in flat result")
		return
	}

	// Compile fetched project
	compiler2, err := tccCompiler.New(
		tccCompiler.WithLocal(gitRootConfig_new),
		tccCompiler.WithBranch(fakeMeta.Repository.Branch),
	)
	if err != nil {
		t.Error(err)
		return
	}

	obj2, _, err := compiler2.Compile(context.Background())
	if err != nil {
		t.Error(err)
		return
	}

	flat2 := obj2.Flat()
	_map2, ok := flat2["object"].(map[string]interface{})
	if !ok {
		t.Error("object not found in flat result")
		return
	}
	if !reflect.DeepEqual(_map, _map2) {

		t.Error("Objects not equal")

		b1, err := json.Marshal(_map)
		if err != nil {
			t.Error(err)
			return
		}
		b2, err := json.Marshal(_map2)
		if err != nil {
			t.Error(err)
			return
		}

		fmt.Println("\n\nB1:\n", string(b1))
		fmt.Println("\n\nB2:\n", string(b2))
		return
	}
}
