package peer

import (
	"context"
	"fmt"
	"testing"

	keypair "github.com/taubyte/tau/p2p/keypair"
)

func TestNewPebblePeer(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	p1, _ := New(
		ctx,
		dir,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11001)},
		nil,
		true,
		false,
	)

	p1.Close()

}
