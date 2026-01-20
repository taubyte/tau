package tests

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"os"
	"regexp"
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	db "github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	"github.com/taubyte/tau/pkg/kvdb"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	service "github.com/taubyte/tau/services/substrate/components/database"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"
)

const (
	projectString    = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	databaseId       = "QmVr37uYcJVNnyFd7zRm2fK66en9fdJ9QvNe5gqEmYTdDc"
	databaseMatch1   = "/test/test1"
	databaseId2      = "QmaCRFcRsv3oNaBRD9XR8mFzmHrkTBGGbkugZfezg9La9K"
	databaseMatch2   = "/literal"
	databaseMatch3   = "/fail"
	kvName           = "testKv"
	kvName2          = "testKvNumber2"
	expectedCommitId = "testCommit2"
)

var (
	expectedString  = "Hello World!"
	expectedString2 = "Hello World Again!"
	expected2       = []byte(expectedString2)
	expected        = []byte(expectedString)
	newDBSize       = 2000000000
	newDBSize2      = 20
	expectedMap     = map[string][]byte{"test": expected, "/expect/1": expected2}
)

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

func TestAll(t *testing.T) {
	meta := patrick.Meta{}
	meta.Repository.ID = 1234567890
	meta.Repository.Branch = "main" // Updated to match repository default branch
	meta.HeadCommit.ID = "commitID"
	meta.Repository.Provider = "github"

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":       {},
			"substrate": {},
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

	// Use a temporary directory to avoid modifying any existing testGIT directories
	gitRoot, err := os.MkdirTemp("", "testGIT-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(gitRoot) // Clean up after test
	gitRootConfig := gitRoot + "/config"
	err = os.MkdirAll(gitRootConfig, 0755)
	assert.NilError(t, err)

	err = gitTest.CloneToDir(u.Context(), gitRootConfig, commonTest.ConfigRepo)
	assert.NilError(t, err)

	// Create TCC compiler
	compiler, err := tccCompiler.New(
		tccCompiler.WithLocal(gitRootConfig),
		tccCompiler.WithBranch(meta.Repository.Branch),
	)
	assert.NilError(t, err)

	// Compile
	obj, validations, err := compiler.Compile(u.Context())
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
		meta.Repository.Branch,
		meta.HeadCommit.ID,
	)
	assert.NilError(t, err)

	context := db.Context{
		ProjectId: projectString,
		Matcher:   databaseMatch1,
	}

	context2 := db.Context{
		ProjectId: projectString,
		Matcher:   databaseMatch2,
	}

	context3 := db.Context{
		ProjectId: projectString,
		Matcher:   databaseMatch3,
	}

	dbFactory := kvdb.New(u.Substrate().Node())
	/************************** Testing New Databases *********************************/
	srv, err := service.New(u.Substrate(), dbFactory)
	assert.NilError(t, err)

	dbNew, err := srv.Database(context)
	assert.NilError(t, err)
	if dbNew == nil {
		t.Error("Creating new database returned nil")
		return
	}

	oldSize, err := dbNew.KV().Size(u.Context())
	assert.NilError(t, err)

	dbNew2, err := srv.Database(context2)
	assert.NilError(t, err)
	if dbNew2 == nil {
		t.Error("Creating new database2 returned nil")
		return
	}

	_, err = srv.Database(context3)
	if err == nil {
		t.Error("should of failed")
		return
	}

	dbExist, err := srv.Database(context)
	assert.NilError(t, err)

	if dbNew != dbExist {
		t.Error("These 2 databases should be equal")
		return
	}

	/************************** Testing List Databases *********************************/
	dbs := srv.Databases()
	if len(dbs) != 2 {
		t.Errorf("Expected 2 database to be registered got %d", len(dbs))
		return
	}

	/************************** Testing KVDB *********************************/
	kv := dbNew.KV()
	if kv == nil {
		t.Error("Keystore for database is nil")
		return
	}

	kv2 := dbNew2.KV()
	if kv2 == nil {
		t.Error("Keystore for database is nil")
		return
	}

	var putInSize uint64
	for key, val := range expectedMap {
		err = kv.Put(u.Context(), key, val)
		assert.NilError(t, err)
		putInSize += uint64(len(val))
	}

	for key, val := range expectedMap {
		value, err := kv.Get(u.Context(), key)
		assert.NilError(t, err)
		if !bytes.Equal(val, value) {
			t.Errorf("Get from database did not match %v != %v", val, value)
			return
		}
	}

	// Making sure kv2 does not connect to kv1
	for key := range expectedMap {
		_, err = kv2.Get(u.Context(), key)
		if err == nil {
			t.Error("expected error")
			return
		}

	}

	resp, err := kv.List(u.Context(), "")
	if err != nil {
		t.Error(err)
		return
	}

	if len(resp) != 2 {
		t.Errorf("Expected 2 entries got %d", len(resp))
		return
	}

	resp, err = kv.List(u.Context(), "expect")
	if err != nil {
		t.Error(err)
		return
	}

	if len(resp) != 1 {
		t.Errorf("Expected pne test entries got %d", len(resp))
		return
	}

	resp, err = kv2.List(u.Context(), "")
	if err != nil {
		t.Error(err)
		return
	}

	if len(resp) != 0 {
		t.Errorf("Expected no entries got %d", len(resp))
		return
	}

	/************************** Testing Changing Size *********************************/
	for key, val := range expectedMap {
		err = kv2.Put(u.Context(), key, val)
		assert.NilError(t, err)
	}

	fs, _, err := tcc.GenerateProject(projectString,
		&structureSpec.Database{
			Id:          databaseId,
			Name:        "testDatabase",
			Description: "",
			Tags:        []string{"test"},
			Match:       "/test/*",
			Regex:       true,
			Local:       false,
			Key:         "",
			Min:         10,
			Max:         50,
			Size:        uint64(newDBSize),
		},
		&structureSpec.Database{
			Id:          databaseId2,
			Name:        "testDatabase2",
			Description: "",
			Tags:        []string{"test"},
			Match:       "/literal",
			Regex:       false,
			Local:       false,
			Key:         "",
			Min:         1,
			Max:         100,
			Size:        uint64(newDBSize2),
		},
	)
	assert.NilError(t, err)

	meta.HeadCommit.ID = expectedCommitId

	// Create TCC compiler for the updated project
	// Since tcc.GenerateProject returns a memfs, we use WithVirtual
	compiler2, err := tccCompiler.New(
		tccCompiler.WithVirtual(fs, "/"),
		tccCompiler.WithBranch(meta.Repository.Branch),
	)
	assert.NilError(t, err)

	// Compile
	obj2, validations2, err := compiler2.Compile(u.Context())
	assert.NilError(t, err)

	// Extract project ID from validations
	projectID2, err := tcc.ExtractProjectID(validations2)
	assert.NilError(t, err)

	// Process DNS validations (dev mode)
	err = tcc.ProcessDNSValidations(
		validations2,
		generatedDomainRegExp,
		true, // dev mode
		nil,  // no DV key needed in dev mode
	)
	assert.NilError(t, err)

	// Extract object and indexes from Flat()
	flat2 := obj2.Flat()
	object2, ok := flat2["object"].(map[string]interface{})
	if !ok {
		t.Error("object not found in flat result")
		return
	}

	indexes2, ok := flat2["indexes"].(map[string]interface{})
	if !ok {
		t.Error("indexes not found in flat result")
		return
	}

	// Publish to TNS
	err = tcc.Publish(
		tns,
		object2,
		indexes2,
		projectID2,
		meta.Repository.Branch,
		meta.HeadCommit.ID,
	)
	assert.NilError(t, err)

	commitId, _, err := tns.Simple().Commit(projectString, "main")
	assert.NilError(t, err)

	if commitId != expectedCommitId {
		t.Errorf("new commit id was not changed %s != %s", commitId, expectedCommitId)
		return
	}

	db1, err := srv.Database(context)
	assert.NilError(t, err)
	if db1 == nil {
		t.Error("Creating new database returned nil")
		return
	}

	newSize, err := db1.KV().Size(u.Context())
	assert.NilError(t, err)
	if newSize <= oldSize {
		t.Errorf("size should be bigger with new config push %d <= %d", newSize, oldSize)
		return
	}

	if newSize != uint64(newDBSize)-putInSize {
		t.Error("newSize was not set properly")
		return
	}

	db2, err := srv.Database(context2)
	assert.NilError(t, err)

	db2Entries, err := db2.KV().List(u.Context(), "")
	assert.NilError(t, err)

	if len(db2Entries) != 2 {
		t.Errorf("expected 2 entries got %d", len(db2Entries))
	}

	entry := make([]byte, 11)
	_, err = rand.Read(entry)
	assert.NilError(t, err)

	err = db2.KV().Put(u.Context(), "fail", entry)
	if err == nil {
		t.Error("expected to fail")
		return
	}

	for key := range expectedMap {
		err = kv2.Delete(u.Context(), key)
		assert.NilError(t, err)
	}

	size, err := db2.KV().Size(u.Context())
	assert.NilError(t, err)
	if size != uint64(newDBSize2) {
		t.Error("size did not get updated for db2")
		return
	}

	err = db2.KV().Put(u.Context(), "pass", entry)
	assert.NilError(t, err)

	data, err := db2.KV().Get(u.Context(), "pass")
	assert.NilError(t, err)

	if !bytes.Equal(data, entry) {
		t.Error("Data from get is not the same")
		return
	}
}
