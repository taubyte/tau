package tns_test

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"gotest.tools/v3/assert"
)

// On a real cloud the builder and the substrate are not always connected:
// the notice must reach the reader through the TNS node between them.
func TestPushReachesACacheNotConnectedToTheWriter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m, err := dream.New(t.Context(), dream.Name("relay"))
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: "relay"})
	assert.NilError(t, err)
	tnsClient := dream.SimpleConfigClients{TNS: &commonIface.ClientConfig{}}.Compat()
	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{"tns": {}},
		Simples: map[string]dream.SimpleConfig{
			"substrate": {Clients: tnsClient},
			"builder":   {Clients: tnsClient},
		},
	}))

	reader, err := u.Simple("substrate")
	assert.NilError(t, err)
	r, err := reader.TNS()
	assert.NilError(t, err)
	writer, err := u.Simple("builder")
	assert.NilError(t, err)
	w, err := writer.TNS()
	assert.NilError(t, err)

	// line topology: builder - tns - substrate
	cut := func() {
		_ = writer.PeerNode().Peer().Network().ClosePeer(reader.PeerNode().ID())
		_ = reader.PeerNode().Peer().Network().ClosePeer(writer.PeerNode().ID())
	}
	cut()

	project, branch := "QmProject", "main"
	publish := func(commit string) {
		cut()
		assert.NilError(t, w.Push(spec.Current(project, branch).Slice(), map[string]string{spec.CurrentCommitPathVariable.String(): commit}))
	}

	publish("c1")
	commit, _, err := r.Simple().Commit(project, branch)
	assert.NilError(t, err)
	assert.Equal(t, commit, "c1")

	publish("c2")
	deadline := time.Now().Add(3 * time.Second)
	for {
		commit, _, err = r.Simple().Commit(project, branch)
		assert.NilError(t, err)
		if commit == "c2" {
			assert.Assert(t, len(writer.PeerNode().Peer().Network().ConnsToPeer(reader.PeerNode().ID())) == 0, "writer and reader got connected")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("3 s after the push the reader still reads commit %q", commit)
		}
		time.Sleep(100 * time.Millisecond)
		cut()
	}
}
