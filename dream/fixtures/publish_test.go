package fixtures

import (
	"context"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	specs "github.com/taubyte/tau/pkg/specs/methods"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	testFixtures "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/tns/dream"
)

func TestUpdate(t *testing.T) {
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
	tns, err := simple.TNS()
	assert.NilError(t, err)

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

	_, err = tns.Fetch(specs.ProjectPrefix(projectID, fakeMeta.Repository.Branch, fakeMeta.HeadCommit.ID))
	if err != nil {
		t.Error(err)
		return
	}
}
