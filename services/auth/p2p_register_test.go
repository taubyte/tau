package auth

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/p2p/streams/command"
)

// Over p2p nothing proves who is asking, so outside dev mode a peer holding
// the swarm key must not be able to (re)register or drop a repository.
func TestRepositoryRegisterOverP2POnlyInDevMode(t *testing.T) {
	srv := &AuthService{devMode: false}
	for _, action := range []string{"register", "unregister"} {
		_, err := srv.apiGitRepositoryServiceHandler(t.Context(), nil, command.Body{
			"action":   action,
			"provider": "github",
			"id":       "12345",
		})
		if err == nil || !strings.Contains(err.Error(), "only allowed in dev mode") {
			t.Fatalf("%s over p2p outside dev mode: got %v, want a refusal", action, err)
		}
	}
}
