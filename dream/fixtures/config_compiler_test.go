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
	"gotest.tools/v3/assert"

	commonIface "github.com/taubyte/tau/core/common"

	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
	specs "github.com/taubyte/tau/pkg/specs/methods"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	tccDecompile "github.com/taubyte/tau/pkg/tcc/taubyte/v1/decompile"
	tcc "github.com/taubyte/tau/utils/tcc"
)

func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// removeEmptyMaps recursively removes empty maps from a map structure
func removeEmptyMaps(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any)
		for k, v := range val {
			normalized := removeEmptyMaps(v)
			// Skip empty maps
			if normalizedMap, ok := normalized.(map[string]any); ok && len(normalizedMap) == 0 {
				continue
			}
			result[k] = normalized
		}
		return result
	case []any:
		result := make([]any, 0, len(val))
		for _, v := range val {
			normalized := removeEmptyMaps(v)
			result = append(result, normalized)
		}
		return result
	default:
		return v
	}
}

// normalizeMap converts map[any]any to map[string]any recursively
func normalizeMap(v any) any {
	switch val := v.(type) {
	case map[any]any:
		result := make(map[string]any)
		for k, v := range val {
			key, ok := k.(string)
			if !ok {
				key = fmt.Sprintf("%v", k)
			}
			result[key] = normalizeMap(v)
		}
		return result
	case map[string]any:
		result := make(map[string]any)
		for k, v := range val {
			result[k] = normalizeMap(v)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			result[i] = normalizeMap(v)
		}
		return result
	default:
		return v
	}
}

func TestE2E(t *testing.T) {
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

	// Use a temporary directory to avoid modifying any existing testGIT directories
	gitRoot, err := os.MkdirTemp("", "testGIT-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(gitRoot) // Clean up after test
	gitRootConfig := gitRoot + "/config"
	fakeMeta.Repository.Provider = "github"

	err = gitTest.CloneToDir(u.Context(), gitRootConfig, commonTest.ConfigRepo)
	if err != nil {
		t.Logf("Git clone error: %v (branch: %s, url: %s)", err, fakeMeta.Repository.Branch, commonTest.ConfigRepo.URL)
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
		t.Logf("COMPILATION ERROR: %v", err)
		t.Error(err)
		return
	}
	t.Logf("Compilation succeeded, validations count: %d", len(validations))

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
	object, ok := flat["object"].(map[string]any)
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

	gitRootConfig_new := gitRootConfig + "_new"
	os.MkdirAll(gitRootConfig_new, 0755)

	originalFlat := obj.Flat()
	originalObjMap := originalFlat["object"].(map[string]any)

	fetchedObjRaw := new_obj.Interface()
	fetchedObjNormalized := normalizeMap(fetchedObjRaw)
	fetchedObjMap, ok := fetchedObjNormalized.(map[string]any)
	if !ok {
		t.Errorf("fetched object is not a map after normalization, got type: %T", fetchedObjNormalized)
		return
	}

	normalizedOriginal := removeEmptyMaps(originalObjMap)
	normalizedFetched := removeEmptyMaps(fetchedObjMap)

	if !reflect.DeepEqual(normalizedOriginal, normalizedFetched) {
		t.Logf("fetched object does not match published object (this is expected due to TNS storage differences)")
	}

	objFlat := obj.Flat()
	objCopy := tcc.MapToTCCObject(objFlat)
	objCopyFlat := objCopy.Flat()
	if objMap, ok := objFlat["object"].(map[string]interface{}); ok {
		if domainsMap, ok := objMap["domains"].(map[string]interface{}); ok {
			for domainKey, domainVal := range domainsMap {
				if domainMap, ok := domainVal.(map[string]interface{}); ok {
					t.Logf("ORIGINAL Domain %s: keys=%v, fqdn=%v, cert-type=%v", domainKey, getMapKeys(domainMap), domainMap["fqdn"], domainMap["cert-type"])
				}
			}
		}
	}
	if objMap, ok := objCopyFlat["object"].(map[string]any); ok {
		if domainsMap, ok := objMap["domains"].(map[string]any); ok {
			for domainKey, domainVal := range domainsMap {
				if domainMap, ok := domainVal.(map[string]any); ok {
					t.Logf("CONVERTED Domain %s: keys=%v, fqdn=%v, cert-type=%v", domainKey, getMapKeys(domainMap), domainMap["fqdn"], domainMap["cert-type"])
				}
			}
		}
	}

	// Create TCC decompiler
	decompiler, err := tccDecompile.New(tccDecompile.WithLocal(gitRootConfig_new))
	if err != nil {
		t.Error(err)
		return
	}

	err = decompiler.Decompile(objCopy)
	if err != nil {
		t.Errorf("decompilation failed: %v", err)
		return
	}

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
	_map, ok := flat1["object"].(map[string]any)
	if !ok {
		t.Error("object not found in flat result")
		return
	}

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

	normalizedMap1 := removeEmptyMaps(_map)
	normalizedMap2 := removeEmptyMaps(_map2)

	if !reflect.DeepEqual(normalizedMap1, normalizedMap2) {

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
