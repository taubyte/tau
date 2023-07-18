package tests

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"bitbucket.org/taubyte/config-compiler/compile"
	"github.com/ipfs/go-datastore"
	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonTest "github.com/taubyte/dreamland/helpers"
	gitTest "github.com/taubyte/dreamland/helpers/git"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/go-interfaces/services/substrate/database"
	"github.com/taubyte/go-interfaces/services/substrate/storage"

	projectLib "github.com/taubyte/go-project-schema/project"
	_ "github.com/taubyte/odo/clients/p2p/hoarder"
	_ "github.com/taubyte/odo/protocols/hoarder/service"
	dbApi "github.com/taubyte/odo/protocols/node/components/database"
	storageApi "github.com/taubyte/odo/protocols/node/components/storage"
	_ "github.com/taubyte/odo/protocols/node/service"
	_ "github.com/taubyte/odo/protocols/seer/service"
	_ "github.com/taubyte/odo/protocols/tns/service"
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

func TestStoring(t *testing.T) {
	u := dreamland.Multiverse("TestStoring")
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder": {Others: map[string]int{"copies": copies}},
			"tns":     {},
			"node":    {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					Hoarder: &commonIface.ClientConfig{},
					TNS:     &commonIface.ClientConfig{},
				},
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

	err = compiler.Publish(simple.TNS())
	if err != nil {
		t.Error(err)
		return
	}

	db, err := dbApi.New(u.Node(), dbApi.Dev())
	if err != nil {
		t.Error(err)
		return
	}

	storageNode, err := storageApi.New(u.Node(), storageApi.Dev())
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
	if strings.HasPrefix(storageMatch, "/") == false {
		storageMatch = "/" + storageMatch
	}

	if strings.HasPrefix(databaseMatch, "/") == false {
		databaseMatch = "/" + databaseMatch
	}

	for _, pid := range pids {
		service, found := u.HoarderByPid(pid)
		if found == false {
			t.Errorf("Hoarder %s not found", pid)
		}

		foundStorage, _ := service.Datastore().Has(u.Context(), datastore.NewKey(fmt.Sprintf("/hoarder/storages/%s%s", storageId, storageMatch)))
		if foundStorage == true {
			storages++
		}

		foundDb, _ := service.Datastore().Has(u.Context(), datastore.NewKey(fmt.Sprintf("/hoarder/databases/%s%s", databaseId, databaseMatch)))
		if foundDb == true {
			databases++
		}

	}

	if databases+storages < 2 {
		t.Errorf("Did not find both storage and database. Storage Found = %d, Database Found = %d", storages, databases)
	}
}
