package tests

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	spec "github.com/taubyte/go-specs/common"
	"github.com/taubyte/go-specs/methods"
	"github.com/taubyte/p2p/peer"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	dreamland "github.com/taubyte/tau/libdream"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	service "github.com/taubyte/tau/protocols/patrick"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/monkey"
	_ "github.com/taubyte/tau/protocols/auth"
	protocolCommon "github.com/taubyte/tau/protocols/common"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/tns"
)

func TestRunWasmRetry(t *testing.T) {
	t.Skip("Review later,  is there a valid reason to retry as now code clones config")

	// Reduce times from minutes to seconds for testing
	service.DefaultReAnnounceFailedJobsTime = 10 * time.Second
	service.DefaultReAnnounceJobTime = 10 * time.Second

	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"patrick": {},
			"auth":    {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	authHttpURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}

	// override ID of project generated so that it matches id in config
	protocolCommon.GetNewProjectID = func(args ...interface{}) string { return commonTest.ProjectID }
	err = commonTest.RegisterTestProject(u.Context(), authHttpURL)
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

	tnsClient := tns.(*tnsClient.Client)
	err = u.RunFixture("pushCode")
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("pushConfig")
	if err != nil {
		t.Error(err)
		return
	}
	// FIXME, reduce this time to 5 seconds and patrick will throw a dead pool error
	time.Sleep(60 * time.Second)
	err = checkAsset(u.Context(), "2463235f-54ad-43bc-b5ad-e466c194de12", spec.DefaultBranch, simple.PeerNode(), tnsClient)
	if err != nil {
		t.Error(err)
		return
	}

	err = checkAsset(u.Context(), "3a1d6781-4a74-42c2-81e0-221f32041825", spec.DefaultBranch, simple.PeerNode(), tnsClient)
	if err != nil {
		t.Error(err)
		return
	}
}

func checkAsset(ctx context.Context, resId, commit string, node peer.Node, tnsClient *tnsClient.Client) error {
	assetHash, err := methods.GetTNSAssetPath(commonTest.ProjectID, resId, commit)
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

	f, err := node.GetFile(ctx, zipCID)
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
