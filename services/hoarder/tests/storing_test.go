package tests

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/go-datastore"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	"github.com/taubyte/tau/pkg/kvdb"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
	dbApi "github.com/taubyte/tau/services/substrate/components/database"
	storageApi "github.com/taubyte/tau/services/substrate/components/storage"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
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

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

// TODO: Fix Hoarder and tests
func TestStoring(t *testing.T) {
	t.Skip("hoarder needs to be fixed")
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dream.Config{
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

	rc, err := compile.CompilerConfig(projectIface, fakJob.Meta, generatedDomainRegExp)
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
