package jobs

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"time"

	compilerCommon "bitbucket.org/taubyte/config-compiler/common"
	commonTest "bitbucket.org/taubyte/dreamland-test/common"
	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	tnsClient "bitbucket.org/taubyte/tns-p2p-client"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/p2p/peer"
	"github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/go-interfaces/services/tns"
	git "github.com/taubyte/go-simple-git"
	"github.com/taubyte/go-specs/methods"
)

func newTestContext(ctx context.Context, simple commonDreamland.Simple, logFile *os.File) testContext {
	tnsClient := simple.TNS().(*tnsClient.Client)
	ctx, ctxC := context.WithCancel(ctx)
	return testContext{
		Context: Context{
			ctx:           ctx,
			ctxC:          ctxC,
			Tns:           tnsClient,
			Node:          simple.GetNode(),
			LogFile:       logFile,
			Monkey:        &mockMonkey{},
			ProjectID:     commonTest.ProjectID,
			OdoClientNode: simple.GetNode(),
		},
	}
}

func (t testContext) testHandler(repoType compilerCommon.RepositoryType, repoId int, job *patrick.Job, preserve bool) func() error {
	return func() error {
		t.RepoType, t.Job = repoType, job

		var url string
		var setConfig bool
		switch repoType {
		case compilerCommon.CodeRepository:
			url = commonTest.CodeRepo.URL
		case compilerCommon.ConfigRepository:
			url = commonTest.ConfigRepo.URL
			setConfig = true
		case compilerCommon.LibraryRepository:
			url = commonTest.LibraryRepo.URL
		case compilerCommon.WebsiteRepository:
			url = commonTest.WebsiteRepo.URL
		default:
			return fmt.Errorf("unknown repo type %d", repoType)
		}
		repo, err := cloneRepo(t.ctx, url, preserve)
		if err != nil {
			return err
		}

		if setConfig {
			configRepoRoot = repo.Root()
		}

		repoPath, err := methods.GetRepositoryPath(testProvider, strconv.Itoa(repoId), t.ProjectID)
		if err != nil {
			return err
		}

		if err = t.Tns.Push(repoPath.Type().Slice(), repoType); err != nil {
			return err
		}

		time.Sleep(time.Second)
		t.WorkDir = repo.Dir()
		t.gitDir = repo.Root()

		handler, err := t.Handler()
		if err != nil {
			return err
		}

		return handler.handle()
	}
}

func (t testContext) library(job *patrick.Job) func() error {
	return t.testHandler(compilerCommon.LibraryRepository, 59371711, job, false)
}

func (t testContext) config(job *patrick.Job) func() error {
	return t.testHandler(compilerCommon.ConfigRepository, 593693892, job, false)
}

func (t testContext) code(job *patrick.Job) func() error {
	return t.testHandler(compilerCommon.CodeRepository, 593693910, job, false)
}

func (t testContext) website(job *patrick.Job) func() error {
	return t.testHandler(compilerCommon.WebsiteRepository, 87654321, job, false)
}

func (m *mockMonkey) Dev() bool {
	return true
}

func newJob(repo commonTest.Repository, jobId string) *patrick.Job {
	return &patrick.Job{
		Logs:     make(map[string]string),
		AssetCid: make(map[string]string),
		Id:       jobId,
		Meta: patrick.Meta{
			Repository: patrick.Repository{
				ID:       repo.ID,
				SSHURL:   fmt.Sprintf("git@github.com:%s/%s", commonTest.GitUser, repo.Name),
				Provider: testProvider,
				Branch:   testBranch,
			},
			HeadCommit: patrick.HeadCommit{
				ID: testCommit,
			},
		},
	}
}

func cloneRepo(ctx context.Context, url string, preserve bool) (*git.Repository, error) {
	repo, err := git.New(ctx,
		git.URL(url),
		git.Token(commonTest.GitToken),
		git.Temporary(),
		git.Preserve(),
	)
	if err != nil {
		return nil, err
	}

	return repo, nil
}

func startDreamland(name string) (u commonDreamland.Universe, err error) {
	u = dreamland.Multiverse(name)
	err = u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder": {},
			"tns":     {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				},
			},
		},
	})

	time.Sleep(2 * time.Second)
	return
}

func checkAssets(node peer.Node, tnsClient tns.Client, resIds []string) error {
	for _, resId := range resIds {
		if err := checkAsset(node, tnsClient, resId); err != nil {
			return err
		}
	}

	return nil
}

func checkAsset(node peer.Node, tnsClient tns.Client, resId string) error {
	assetHash, err := methods.GetTNSAssetPath(commonTest.ProjectID, resId, testBranch)
	if err != nil {
		return err
	}

	buildZipCID, err := tnsClient.Fetch(assetHash)
	if err != nil {
		return err
	}

	zipCID, ok := buildZipCID.Interface().(string)
	if !ok {
		return fmt.Errorf("Could not fetch build cid: %s", buildZipCID)
	}

	f, err := node.GetFile(node.Context(), zipCID)
	if err != nil {
		return err
	}

	fileBytes, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}

	if len(fileBytes) == 0 {
		return fmt.Errorf("File for `%s` is empty", resId)
	}

	return nil
}
