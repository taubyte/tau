//go:build web3

package tests

import (
	"context"
	"os"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

// TestMain for the web3 build: same backend mocks as the default build plus the
// ipfs node, so the ipfs + ethereum plugins (web3-only factories) are wired.
func TestMain(m *testing.M) {
	if err := plugins.Initialize(context.Background(),
		plugins.DatabaseNode(&mockDBService{}),
		plugins.PubsubNode(pubsubMock),
		plugins.P2PNode(p2pMock),
		plugins.StorageNode(&mockStorageService{}),
		plugins.IpfsNode(&mockIpfsService{}),
	); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
