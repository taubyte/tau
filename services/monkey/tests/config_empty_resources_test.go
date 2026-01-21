package tests

import (
	"context"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	"github.com/taubyte/tau/utils/id"
	tccUtils "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/tns/dream"
)

// TestConfigJobEmptyResources tests that indexes always exist in the flat result,
// even when a project has no resources.
func TestConfigJobEmptyResources(t *testing.T) {
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
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	tnsClient, err := simple.TNS()
	assert.NilError(t, err)

	projectID := id.Generate("")
	fs, prj, err := tccUtils.GenerateProject(projectID)
	assert.NilError(t, err)

	// Verify project has no resources
	getter := prj.Get()
	_, globalFuncs := getter.Functions("")
	assert.Equal(t, len(globalFuncs), 0, "project should have no functions")
	_, globalDBs := getter.Databases("")
	assert.Equal(t, len(globalDBs), 0, "project should have no databases")
	_, globalStorages := getter.Storages("")
	assert.Equal(t, len(globalStorages), 0, "project should have no storages")
	_, globalWebsites := getter.Websites("")
	assert.Equal(t, len(globalWebsites), 0, "project should have no websites")
	_, globalLibraries := getter.Libraries("")
	assert.Equal(t, len(globalLibraries), 0, "project should have no libraries")
	_, globalMessaging := getter.Messaging("")
	assert.Equal(t, len(globalMessaging), 0, "project should have no messaging")
	_, globalSmartOps := getter.SmartOps("")
	assert.Equal(t, len(globalSmartOps), 0, "project should have no smartops")
	_, globalDomains := getter.Domains("")
	assert.Equal(t, len(globalDomains), 0, "project should have no domains")

	compiler, err := tccCompiler.New(
		tccCompiler.WithVirtual(fs, "/"),
		tccCompiler.WithBranch("main"),
	)
	assert.NilError(t, err)

	// Compile
	obj, validations, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	// Extract project ID from validations
	extractedProjectID, err := tccUtils.ExtractProjectID(validations)
	assert.NilError(t, err)
	assert.Equal(t, extractedProjectID, projectID)

	err = tccUtils.ProcessDNSValidations(
		validations,
		generatedDomainRegExp,
		true,
		nil,
	)
	assert.NilError(t, err)

	// Extract object and indexes from Flat()
	flat := obj.Flat()
	object, ok := flat["object"].(map[string]interface{})
	assert.Assert(t, ok, "object not found in flat result")

	indexes, ok := flat["indexes"].(map[string]interface{})
	assert.Assert(t, ok, "indexes not found in flat result")
	assert.Equal(t, len(indexes), 0, "indexes should be empty when there are no resources")

	// Publish to TNS
	err = tccUtils.Publish(
		tnsClient,
		object,
		indexes,
		extractedProjectID,
		"main",
		"test-commit-id",
	)
	assert.NilError(t, err)
}
