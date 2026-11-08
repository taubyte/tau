package pubsub

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/structure"
)

// A channel declared by a messaging resource can be published to even when no
// function or websocket bridge listens on it.
func TestPublish_DeclaredChannelWithoutListeners(t *testing.T) {
	const app = "someApp"
	messaging := map[string]structureSpec.Messaging{"id": {Name: "m", Match: testChannel}}
	httpOnly := map[string]structureSpec.Function{"f": {Name: "f", Type: "https"}}
	empty := map[string]structureSpec.Messaging{}

	for name, global := range map[string]bool{"project": true, "application": false} {
		t.Run(name, func(t *testing.T) {
			s := NewTestService(peer.Mock(t.Context()))
			structure.RefreshTestVariables()
			structure.FakeFetchMethod = func(path tns.Path) (tns.Object, error) {
				p := path.Slice()
				switch {
				case p[len(p)-1] == "current":
					return structure.ResponseObject{Object: testCommit}, nil
				case p[len(p)-1] == "functions" && p[6] == "functions":
					return structure.ResponseObject{Object: httpOnly}, nil
				case p[len(p)-1] == "functions":
					return structure.ResponseObject{Object: map[string]structureSpec.Function{}}, nil
				case p[len(p)-1] != "messaging":
					return nil, errors.New("not found")
				case global == (p[6] == "messaging"):
					return structure.ResponseObject{Object: messaging}, nil
				}
				return structure.ResponseObject{Object: empty}, nil
			}

			require.NoError(t, s.Publish(t.Context(), testProject, app, "res", testChannel, []byte("x")))
			require.Error(t, s.Publish(t.Context(), testProject, app, "res", "undeclared", []byte("x")), "a channel no messaging declares must still fail")
		})
	}
}
