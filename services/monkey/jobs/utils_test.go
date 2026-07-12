//go:build dreaming

package jobs

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/common/repositorytype"
	"github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/pkg/specs/methods"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func newTestContext(ctx context.Context, simple *dream.Simple, logFile *os.File) testContext {
	tns, err := simple.TNS()
	if err != nil {
		panic(err)
	}

	ctx, ctxC := context.WithCancel(ctx)
	return testContext{
		Context: Context{
			ctx:        ctx,
			ctxC:       ctxC,
			Tns:        tns,
			Node:       simple.PeerNode(),
			LogFile:    logFile,
			Monkey:     &mockMonkey{},
			ProjectID:  commonTest.ProjectID,
			ClientNode: simple.PeerNode(),
		},
	}
}

func (t testContext) testHandler(repoType repositorytype.Type, repoId int, job *patrick.Job, preserve bool) func() error {
	return func() error {
		t.RepoType, t.Job = repoType, job

		var url string
		var setConfig bool
		switch repoType {
		case repositorytype.CodeRepository:
			url = commonTest.CodeRepo.URL
		case repositorytype.ConfigRepository:
			url = commonTest.ConfigRepo.URL
			setConfig = true
		case repositorytype.LibraryRepository:
			url = commonTest.LibraryRepo.URL
		case repositorytype.WebsiteRepository:
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

		// Confirm the repo-type push landed in tns before the handler relies
		// on classification data derived from it.
		for deadline := time.Now().Add(2 * time.Second); ; {
			if _, fetchErr := t.Tns.Fetch(repoPath.Type()); fetchErr == nil {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("repo type for %v not visible in tns", repoPath.Type())
			}
			time.Sleep(100 * time.Millisecond)
		}
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
	return t.testHandler(repositorytype.LibraryRepository, 59371711, job, false)
}

func (t testContext) config(job *patrick.Job) func() error {
	return t.testHandler(repositorytype.ConfigRepository, 593693892, job, false)
}

func (t testContext) code(job *patrick.Job) func() error {
	return t.testHandler(repositorytype.CodeRepository, 593693910, job, false)
}

func (t testContext) website(job *patrick.Job) func() error {
	return t.testHandler(repositorytype.WebsiteRepository, 87654321, job, false)
}

func (m *mockMonkey) Dev() bool {
	return true
}

func (m *mockMonkey) Delete(jid string) {
}

func (m *mockMonkey) Hoarder() hoarder.Client {
	return m.hoarder
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

func startDream(t *testing.T) (u *dream.Universe, cleanup func(), err error) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	cleanup = func() {
		m.Close()
	}

	u, err = m.New(dream.UniverseConfig{Name: t.Name()})
	if err != nil {
		return nil, cleanup, err
	}

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder": {},
			"tns":     {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})

	// give the client time to discover both services
	if simple, simpleErr := u.Simple("client"); simpleErr == nil {
		clientNode := simple.PeerNode()
		var targets []peer.Node
		if h := u.Hoarder(); h != nil {
			targets = append(targets, h.Node())
		}
		if t := u.TNS(); t != nil {
			targets = append(targets, t.Node())
		}
		for deadline := time.Now().Add(4 * time.Second); ; {
			allConnected := true
			for _, target := range targets {
				if clientNode.Peer().Network().Connectedness(target.ID()) != network.Connected {
					allConnected = false
					break
				}
			}
			if allConnected || time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
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

	fileBytes, err := io.ReadAll(f)
	if err != nil {
		return err
	}

	if len(fileBytes) == 0 {
		return fmt.Errorf("File for `%s` is empty", resId)
	}

	return nil
}
