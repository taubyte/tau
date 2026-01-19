package fixtures

import (
	"context"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/dream"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	testFixtures "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	"github.com/taubyte/tau/utils/maps"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

func TestIndexer(t *testing.T) {
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
	if err != nil {
		t.Error(err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	fs, err := testFixtures.VirtualFSWithBuiltProject()
	if err != nil {
		t.Error(err)
		return
	}

	// Create TCC compiler
	compiler, err := tccCompiler.New(
		tccCompiler.WithVirtual(fs, "/test_project/config"),
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

	tnsClient, err := simple.TNS()
	assert.NilError(t, err)

	// Publish to TNS
	err = tcc.Publish(
		tnsClient,
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

	resp, err := tnsClient.Lookup(tns.Query{Prefix: []string{"domains"}, RegEx: false})
	if err != nil {
		t.Error(err)
		return
	}

	_map := make(map[string]interface{})
	_map["test"] = resp
	list, err := maps.StringArray(_map, "test")
	if err != nil {
		t.Error(err)
		return
	}

	if len(list) != 2 { // local/global domains index, project,branch
		t.Errorf("Expected 2 got %d", len(list))
	}
}
