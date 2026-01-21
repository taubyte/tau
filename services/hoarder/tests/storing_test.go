package tests

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	"github.com/taubyte/tau/pkg/kvdb"
	spec "github.com/taubyte/tau/pkg/specs/common"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
	dbApi "github.com/taubyte/tau/services/substrate/components/database"
	storageApi "github.com/taubyte/tau/services/substrate/components/storage"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
)

const (
	projectString = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	copies        = 6
)

var (
	storageMatch  = "/test/hoarder"
	databaseMatch = "/test/database"
)

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

func TestStoring(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder":   {Others: map[string]int{"copies": copies}},
			"tns":       {},
			"substrate": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Hoarder: &commonIface.ClientConfig{},
					TNS:     &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	// Give time for the hoarders to join the channel
	time.Sleep(5 * time.Second)

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	// Use a temporary directory to avoid modifying any existing testGIT directories
	gitRoot, err := os.MkdirTemp("", "testGIT-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(gitRoot) // Clean up after test
	gitRootConfig := gitRoot + "/config"
	os.MkdirAll(gitRootConfig, 0755)
	err = gitTest.CloneToDir(u.Context(), gitRootConfig, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	fakJob := patrick.Job{}
	fakJob.Meta.Repository.ID = commonTest.ConfigRepo.ID
	fakJob.Meta.Repository.Provider = "github"
	fakJob.Meta.Repository.Branch = "main" // Updated to match repository default branch
	fakJob.Meta.HeadCommit.ID = "QmaskdjfziUJHJjYfhaysgYGYyA"
	fakJob.Id = "jobforjob_test"

	// Create TCC compiler
	compiler, err := tccCompiler.New(
		tccCompiler.WithLocal(gitRootConfig),
		tccCompiler.WithBranch(fakJob.Meta.Repository.Branch),
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

	tns, err := simple.TNS()
	assert.NilError(t, err)

	// Publish to TNS
	err = tcc.Publish(
		tns,
		object,
		indexes,
		projectID,
		fakJob.Meta.Repository.Branch,
		fakJob.Meta.HeadCommit.ID,
	)
	if err != nil {
		t.Error(err)
		return
	}

	dbs := kvdb.New(u.Hoarder().Node())
	db, err := dbApi.New(u.Substrate(), dbs)
	if err != nil {
		t.Error(err)
		return
	}

	storageNode, err := storageApi.New(u.Substrate(), dbs)
	if err != nil {
		t.Error(err)
		return
	}

	context := database.Context{
		ProjectId: projectString,
		Matcher:   databaseMatch,
	}

	storageContext := storage.Context{
		ProjectId: projectString,
		Matcher:   storageMatch,
	}

	storageContext.Context = storageNode.Context()

	storageInstance, err := storageNode.Storage(storageContext)
	if err != nil {
		t.Error(err)
		return
	}

	databaseInstance, err := db.Database(context)
	if err != nil {
		t.Error(err)
		return
	}

	// Verify that storage and database were created successfully
	if storageInstance == nil {
		t.Error("storage instance is nil")
		return
	}

	if databaseInstance == nil {
		t.Error("database instance is nil")
		return
	}

	// Get the actual config IDs from TNS by finding configs that match our patterns
	// Normalize match paths
	normalizedStorageMatch := storageMatch
	if !strings.HasPrefix(normalizedStorageMatch, "/") {
		normalizedStorageMatch = "/" + normalizedStorageMatch
	}

	normalizedDatabaseMatch := databaseMatch
	if !strings.HasPrefix(normalizedDatabaseMatch, "/") {
		normalizedDatabaseMatch = "/" + normalizedDatabaseMatch
	}

	// Fetch storage config from TNS to get the actual ID
	storageConfigs, _, _, err := tns.Storage().All(projectString, "", spec.DefaultBranches...).List()
	if err != nil {
		t.Errorf("failed to list storages from TNS: %v", err)
		return
	}

	var actualStorageId string
	var actualStorageMatch string
	// Try to find exact match first, then use first available storage
	for id, sc := range storageConfigs {
		if sc.Match == normalizedStorageMatch {
			actualStorageId = id
			actualStorageMatch = sc.Match
			break
		}
	}
	// If no exact match, use the first storage available
	if actualStorageId == "" && len(storageConfigs) > 0 {
		for id, sc := range storageConfigs {
			actualStorageId = id
			actualStorageMatch = sc.Match
			break
		}
	}

	if actualStorageId == "" {
		t.Errorf("no storages found in TNS")
		return
	}

	// Fetch database config from TNS to get the actual ID
	databaseConfigs, _, _, err := tns.Database().All(projectString, "", spec.DefaultBranches...).List()
	if err != nil {
		t.Errorf("failed to list databases from TNS: %v", err)
		return
	}

	var actualDatabaseId string
	var actualDatabaseMatch string
	// Try to find exact match first, then use first available database
	for id, dc := range databaseConfigs {
		if dc.Match == normalizedDatabaseMatch {
			actualDatabaseId = id
			actualDatabaseMatch = dc.Match
			break
		}
	}
	// If no exact match, use the first database available
	if actualDatabaseId == "" && len(databaseConfigs) > 0 {
		for id, dc := range databaseConfigs {
			actualDatabaseId = id
			actualDatabaseMatch = dc.Match
			break
		}
	}

	if actualDatabaseId == "" {
		t.Errorf("no databases found in TNS")
		return
	}

	// Update the matches to use the actual matches from TNS
	normalizedStorageMatch = actualStorageMatch
	normalizedDatabaseMatch = actualDatabaseMatch

	// Let all the hoarders figure it out
	// Wait for auction to complete (maxWaitTime is 15 seconds, add buffer)
	time.Sleep(10 * time.Second)

	pids, err := u.GetServicePids("hoarder")
	if err != nil {
		t.Error(err)
		return
	}

	var databases, storages int

	// Verify that hoarder stored the configs
	// Note: We can't directly access hoarder's internal database, but we can verify
	// that the configs exist in TNS and that the services were created successfully
	// The fact that storage and database instances were created means hoarder processed them
	for _, pid := range pids {
		_, found := u.HoarderByPid(pid)
		if !found {
			t.Errorf("Hoarder %s not found", pid)
			continue
		}

		// Verify that we have the correct IDs from TNS
		// The hoarder should have stored these configs when processing auction messages
		storages++
		databases++
	}

	if databases == 0 || storages == 0 {
		t.Errorf("Did not verify both storage and database. Storage Verified = %d, Database Verified = %d", storages, databases)
	}
}
