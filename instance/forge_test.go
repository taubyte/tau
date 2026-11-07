package instance

import (
	"net"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/forge/drivers"

	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/services/plm/ingress"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

func TestAFrontendFollowsAForgesEventStream(t *testing.T) {
	sshd, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { sshd.Close() })
	dialled := make(chan struct{}, 1)
	go func() {
		conn, err := sshd.Accept()
		if err != nil {
			return
		}
		conn.Close()
		dialled <- struct{}{}
	}()

	priv, pub := gittest.KeyPair(t)
	n, _, _ := makeNode(t, Config{
		Services: []string{Stats, PLM},
		Sections: map[string]any{PLM: ingress.Config{Listen: "127.0.0.1:0"}},
		Forges: []drivers.Forge{{
			Host: "review.example.com", Driver: drivers.Gerrit,
			Events:   &drivers.Events{Address: sshd.Addr().String(), User: "tau-bot", Key: priv},
			HostKeys: []string{string(gossh.MarshalAuthorizedKey(pub))},
		}},
	})
	require.NoError(t, n.Start(t.Context()))

	select {
	case <-dialled:
	case <-time.After(10 * time.Second):
		t.Fatal("ingress never dialled the forge's event stream")
	}
}
