//go:build !web3

package tests

import (
	"context"
	"os"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

// TestMain initializes the plugin singleton once with the backend mocks so
// every test shares them. No-backend tests ignore the nodes; backend tests use
// them. Guests get isolated state (mocks return fresh stores per call). The
// web3 build wires the extra ipfs node — see main_web3_test.go.
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
