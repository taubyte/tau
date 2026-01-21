package fixtures

import (
	"context"
	"os"
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	specs "github.com/taubyte/tau/pkg/specs/methods"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	tccDecompile "github.com/taubyte/tau/pkg/tcc/taubyte/v1/decompile"
	_ "github.com/taubyte/tau/services/tns/dream"
	"github.com/taubyte/tau/utils/maps"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

func TestDecompileProd(t *testing.T) {
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

	// Use a temporary directory to avoid modifying any existing testGIT directories
	gitRoot, err := os.MkdirTemp("", "testGIT-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(gitRoot) // Clean up after test
	gitRootConfig := gitRoot + "/prodConfigDream"
	os.MkdirAll(gitRootConfig, 0755)

	fakeMeta := commonTest.ConfigRepo.HookInfo
	fakeMeta.Repository.Branch = "main" // Updated to match repository default branch
	fakeMeta.Repository.Provider = "github"

	err = gitTest.CloneToDir(u.Context(), gitRootConfig, commonTest.ConfigRepo)
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

	// Use a temporary directory for decompilation output
	testProjectDir, err := os.MkdirTemp("", "testDecompileProd-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(testProjectDir) // Clean up after test

	// Convert the compiled object's flat structure to a TCC object for decompilation
	// Note: We use the compiled object's flat structure (which includes both "object" and "indexes")
	// rather than the fetched object from TNS, since the fetched object is just the "object" part
	// and TCC decompiler expects the full structure with "object" and "indexes" as top-level children.
	// This is consistent with how config_compiler_test.go handles decompilation.
	objFlat := obj.Flat()

	// Create a TCC object from the flat structure (which includes both object and indexes)
	objCopy := tcc.MapToTCCObject(objFlat)

	// Create TCC decompiler
	decompiler, err := tccDecompile.New(tccDecompile.WithLocal(testProjectDir))
	if err != nil {
		t.Error(err)
		return
	}

	// Decompile the object to filesystem
	// Note: Decompile modifies the object in place, so we use the copy
	err = decompiler.Decompile(objCopy)
	if err != nil {
		t.Errorf("decompilation failed: %v", err)
		return
	}

}
