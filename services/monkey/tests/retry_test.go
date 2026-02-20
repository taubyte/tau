//go:build dreaming

package tests

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/p2p/peer"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	service "github.com/taubyte/tau/services/patrick"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/monkey/dream"
	_ "github.com/taubyte/tau/services/auth/dream"
	protocolCommon "github.com/taubyte/tau/services/common"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func TestRunWasmRetry_Dreaming(t *testing.T) {
	t.Skip("Review later,  is there a valid reason to retry as now code clones config")

	// Reduce times from minutes to seconds for testing
	service.DefaultReAnnounceJobTime = 10 * time.Second

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"patrick": {},
			"auth":    {},
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

	mockAuth, err := simple.Auth()
	assert.NilError(t, err)

	// override ID of project generated so that it matches id in config
	protocolCommon.GetNewProjectID = func(args ...interface{}) string { return commonTest.ProjectID }
	err = commonTest.RegisterTestProject(u.Context(), mockAuth)
	assert.NilError(t, err)

	tns, err := simple.TNS()
	assert.NilError(t, err)

	tnsClient := tns.(*tnsClient.Client)
	err = u.RunFixture("pushCode")
	assert.NilError(t, err)

	err = u.RunFixture("pushConfig")
	assert.NilError(t, err)

	// FIXME, reduce this time to 5 seconds and patrick will throw a dead pool error
	time.Sleep(60 * time.Second)
	err = checkAsset(u.Context(), "2463235f-54ad-43bc-b5ad-e466c194de12", spec.DefaultBranches, simple.PeerNode(), tnsClient)
	assert.NilError(t, err)

	err = checkAsset(u.Context(), "3a1d6781-4a74-42c2-81e0-221f32041825", spec.DefaultBranches, simple.PeerNode(), tnsClient)
	assert.NilError(t, err)
}

func checkAsset(ctx context.Context, resId string, branches []string, node peer.Node, tnsClient *tnsClient.Client) (err error) {
	for _, branch := range branches {
		if err = checkAssetOnBranch(ctx, resId, branch, node, tnsClient); err == nil {
			return
		}
	}
	return
}

func checkAssetOnBranch(ctx context.Context, resId, branch string, node peer.Node, tnsClient *tnsClient.Client) error {
	assetHash, err := methods.GetTNSAssetPath(commonTest.ProjectID, resId, branch)
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
