//go:build web3

package tests

import (
	"context"
	"os"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

// TestMain for the web3 build: same backend mocks as the default build. The
// ipfs and ethereum plugins were extracted to standalone vm-orbit satellites
// (github.com/taubyte/orbit-ipfs, github.com/taubyte/orbit-eth).
func TestMain(m *testing.M) {
	if err := plugins.Initialize(context.Background(),
		plugins.DatabaseNode(&mockDBService{}),
		plugins.PubsubNode(pubsubMock),
		plugins.P2PNode(p2pMock),
		plugins.StorageNode(&mockStorageService{}),
	); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
