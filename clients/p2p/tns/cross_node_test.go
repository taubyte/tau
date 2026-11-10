package tns_test

import (
	"reflect"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	_ "github.com/taubyte/tau/services/tns/dream"
	"gotest.tools/v3/assert"
)

// A substrate caches the current commit and asset CIDs it read. When another
// node (the builder) publishes a new version the way the config compiler and
// the website build do, the substrate must see it at once, not when its cache
// expires two minutes later.
func TestPushFromAnotherNodeReachesTheCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m, err := dream.New(t.Context(), dream.Name("crossnode"))
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: "crossnode"})
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

	// on Dream every node is connected to every other one
	assert.NilError(t, writer.PeerNode().Peer().Connect(t.Context(), peerCore.AddrInfo{ID: reader.PeerNode().ID(), Addrs: reader.PeerNode().Peer().Addrs()}))

	project, branch := "QmProject", "main"
	host, err := methods.HttpPath("abc.default.localtau", "websites")
	assert.NilError(t, err)
	links := host.Versioning().Links()
	asset, err := methods.GetTNSAssetPath(project, "QmSite1", branch)
	assert.NilError(t, err)
	publish := func(commit, cid string) {
		assert.NilError(t, w.Push([]string{}, map[string]interface{}{links.String(): []string{commit}}))
		assert.NilError(t, w.Push([]string{"projects", project, "branches", branch, "commit", commit}, map[string]interface{}{"x": commit}))
		assert.NilError(t, w.Push(spec.Current(project, branch).Slice(), map[string]string{spec.CurrentCommitPathVariable.String(): commit}))
		assert.NilError(t, w.Push(asset.Slice(), cid))
	}

	publish("c1", "cid1")
	commit, _, err := r.Simple().Commit(project, branch)
	assert.NilError(t, err)
	assert.Equal(t, commit, "c1")
	obj, err := r.Fetch(asset)
	assert.NilError(t, err)
	assert.Equal(t, obj.Interface(), "cid1")

	obj, err = r.Fetch(links)
	assert.NilError(t, err)
	assert.DeepEqual(t, obj.Interface(), []interface{}{"c1"})

	publish("c2", "cid2")
	deadline := time.Now().Add(2 * time.Second)
	for {
		commit, _, err = r.Simple().Commit(project, branch)
		assert.NilError(t, err)
		obj, err = r.Fetch(asset)
		assert.NilError(t, err)
		cid := obj.Interface()
		obj, err = r.Fetch(links)
		assert.NilError(t, err)
		index := obj.Interface()
		if commit == "c2" && cid == "cid2" && reflect.DeepEqual(index, []interface{}{"c2"}) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("2 s after the push the substrate still reads commit %q, asset %v, routing index %v", commit, cid, index)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
