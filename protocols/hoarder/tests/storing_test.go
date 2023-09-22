package tests

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/go-datastore"
	"github.com/taubyte/config-compiler/compile"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/go-interfaces/services/substrate/components/database"
	"github.com/taubyte/go-interfaces/services/substrate/components/storage"
	dreamland "github.com/taubyte/tau/libdream"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	gitTest "github.com/taubyte/tau/libdream/helpers/git"
	"gotest.tools/v3/assert"

	projectLib "github.com/taubyte/go-project-schema/project"
	_ "github.com/taubyte/tau/clients/p2p/hoarder"
	"github.com/taubyte/tau/pkgs/kvdb"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/seer"
	_ "github.com/taubyte/tau/protocols/substrate"
	dbApi "github.com/taubyte/tau/protocols/substrate/components/database"
	storageApi "github.com/taubyte/tau/protocols/substrate/components/storage"
	_ "github.com/taubyte/tau/protocols/tns"
)

const (
	projectString = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	copies        = 6

	databaseId = "QmVr37uYcJVNnyFd7zRm2fK66en9fdJ9QvNe5gqEmYTdDc"
	storageId  = "QmT8paeNbcZcm8TsrN26bixehsdU2JjiBSr6bjBBFmxhGM"
)

var (
	storageMatch  = "/test/hoarder"
	databaseMatch = "/test/database"
)

// TODO: Fix Hoarder and tests
func TestStoring(t *testing.T) {
	t.Skip("hoarder needs to be fixed")
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder":   {Others: map[string]int{"copies": copies}},
			"tns":       {},
			"substrate": {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
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
	gitRoot := "./testGIT"

	defer os.RemoveAll(gitRoot)
	gitRootConfig := gitRoot + "/config"
	os.MkdirAll(gitRootConfig, 0755)
	err = gitTest.CloneToDirSSH(u.Context(), gitRootConfig, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	projectIface, err := projectLib.Open(projectLib.SystemFS(gitRootConfig))
	if err != nil {
		t.Error(err)
		return
	}

	fakJob := patrick.Job{}
	fakJob.Meta.Repository.ID = commonTest.ConfigRepo.ID
	fakJob.Meta.Repository.Provider = "github"
	fakJob.Meta.Repository.Branch = "master"
	fakJob.Meta.HeadCommit.ID = "QmaskdjfziUJHJjYfhaysgYGYyA"
	fakJob.Id = "jobforjob_test"

	rc, err := compile.CompilerConfig(projectIface, fakJob.Meta)
	if err != nil {
		t.Error(err)
		return
	}

	compiler, err := compile.New(rc, compile.Dev())
	if err != nil {
		t.Error(err)
		return
	}

	defer compiler.Close()

	err = compiler.Build()
	if err != nil {
		t.Error(err)
		return
	}

	tns, err := simple.TNS()
	assert.NilError(t, err)

	err = compiler.Publish(tns)
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

	_, err = storageNode.Storage(storageContext)
	if err != nil {
		t.Error(err)
		return
	}

	_, err = db.Database(context)
	if err != nil {
		t.Error(err)
		return
	}

	// Let all the hoarders figure it out
	time.Sleep(30 * time.Second)

	pids, err := u.GetServicePids("hoarder")
	if err != nil {
		t.Error(err)
		return
	}

	var databases, storages int

	// Incase anyone changes test match to not have /
	if !strings.HasPrefix(storageMatch, "/") {
		storageMatch = "/" + storageMatch
	}

	if !strings.HasPrefix(databaseMatch, "/") {
		databaseMatch = "/" + databaseMatch
	}

	for _, pid := range pids {
		service, found := u.HoarderByPid(pid)
		if !found {
			t.Errorf("Hoarder %s not found", pid)
		}

		key := datastore.NewKey(fmt.Sprintf("/hoarder/storages/%s%s", storageId, storageMatch))
		storage, err := service.Node().GetFile(u.Context(), key.String())
		fmt.Println("STORAGE:::", storage, err)

		key = datastore.NewKey(fmt.Sprintf("/hoarder/databases/%s%s", databaseId, databaseMatch))
		db, err := service.Node().GetFile(u.Context(), key.String())
		fmt.Println("DB::::", db, err)
	}

	if databases+storages < 2 {
		t.Errorf("Did not find both storage and database. Storage Found = %d, Database Found = %d", storages, databases)
	}
}
