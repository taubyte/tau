//go:build dreaming

package dream_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// views_test holds projects/with/views and projects/refused/views: a project cut into
// one view per region, deployed into a universe standing in for each region in turn.
//
// THE CUT IS THE ONE A DEPLOY PUBLISHES (schema.Views, through publish), and a node
// reads its own region's view (node.AsDestination). So what a region serves here is
// what it would serve on a cloud with both: eu answers the route, because the function
// and the gateway in front of it are placed there; us holds the cache and the zone and
// no route at all.
var twoRegions = schema.Preview("eu", "us")

// inRegion starts a universe standing in for one region and publishes the project into
// it, cut for twoRegions.
func inRegion(t *testing.T, region, name string) (*running, *node.Node) {
	t.Helper()
	u, err := nodeDream.Start(t.Context(), t.Name()+"-"+region, nodeDream.Config{
		Services: []string{node.Register, node.Worker, node.Discovery, node.Gateway, node.Database, node.Primitives},
		Options:  []node.Option{node.AsDestination(region)},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	pids := u.Pids()
	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)
	cid, err := store.Put(t.Context(), bytes.NewReader(handlerWASM(t)))
	require.NoError(t, err)

	r := publish(t, pids[:1], deployed{project: "shop", name: name, host: "regional.example.com", regions: twoRegions}, artifacts{"": cid})[0]
	return r, r.node
}

// viewHeld is what a region's view of the shop deployment holds, by kind/name.
func viewHeld(t *testing.T, n *node.Node, region string) (map[string]bool, uint64) {
	t.Helper()
	v, err := n.Register().Local().ResolveView(t.Context(), "shop", "prod", region)
	require.NoError(t, err)
	body, err := n.Register().Local().Object(t.Context(), v.Object)
	require.NoError(t, err)
	var obj map[string]any
	require.NoError(t, json.Unmarshal(body, &obj))
	out := map[string]bool{}
	for _, res := range schema.Resources(obj) {
		out[res.Kind+"/"+res.Name] = true
	}
	return out, v.Revision
}

func TestEachRegionServesItsOwnView(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	eu, euNode := inRegion(t, "eu", "with/views")
	held, _ := viewHeld(t, euNode, "eu")
	require.Equal(t, map[string]bool{"function/hello": true, "gateway/regional": true, "zone/example_com": true, "cache/sessions": true}, held)

	var said string
	require.Eventually(t, func() bool {
		got := eu.ask(t, http.MethodGet, "/")
		said = got.Body.String()
		return got.Code == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond, "eu answers the route it holds: %s", &lastSaid{&said})

	// US HOLDS WHAT RUNS ANYWHERE, and nothing placed in eu: no function, so no gateway
	// in front of one, so the name it answers on reaches no route.
	us, usNode := inRegion(t, "us", "with/views")
	held, _ = viewHeld(t, usNode, "us")
	require.Equal(t, map[string]bool{"zone/example_com": true, "cache/sessions": true}, held)
	got := us.ask(t, http.MethodGet, "/")
	require.NotEqual(t, http.StatusOK, got.Code, "us holds no route, and does not borrow eu's")

	as, err := usNode.Register().Local().Assigned(t.Context(), "us")
	require.NoError(t, err)
	require.Len(t, as, 1, "us learns of the deployment from its own index")

	// A RESOURCE REMOVED IN A LATER REVISION LEAVES ITS REGION'S VIEW: the cache goes,
	// us still holds the deployment, at the new revision, without it.
	later := t.TempDir()
	require.NoError(t, os.CopyFS(later, os.DirFS(projectDir("with/views"))))
	require.NoError(t, os.Remove(filepath.Join(later, "cache", "sessions.yaml")))
	compiled, _, _ := projectAs(t, later, artifacts{"": "unused"}, twoRegions)
	var flat map[string]any
	require.NoError(t, json.Unmarshal(compiled, &flat))
	_, err = usNode.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 2, Compiled: compiled,
		Hosts: []string{"regional.example.com"}, Views: cutFor(t, flat, twoRegions),
	})
	require.NoError(t, err)
	held, revision := viewHeld(t, usNode, "us")
	require.EqualValues(t, 2, revision)
	require.Equal(t, map[string]bool{"zone/example_com": true}, held)
}

// AN EDGE ACROSS REGIONS IS REFUSED: the cut a deploy publishes cannot be made, so
// nothing is published for either region.
func TestAProjectReachingAcrossRegionsIsRefused(t *testing.T) {
	c, err := schema.New(schema.WithLocal(projectDir("refused/views/across")), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err, "it compiles; the refusal is the cut's")

	_, findings, err := schema.Views(obj.Flat(), schema.WithRegions(twoRegions))
	require.ErrorIs(t, err, schema.ErrUnviewable)
	var broken []string
	for _, f := range findings {
		if f.Severity == schema.Broken {
			broken = append(broken, f.Kind+"/"+f.Name+": "+f.Title)
		}
	}
	require.Equal(t, []string{`function/reader: reaches cache/sessions_eu, which is not placed in "us" where this is`}, broken)

	// AS EU ALONE, the same project is placed: a stand-in for one region refuses only
	// the region it does not serve, and says it by name.
	_, findings, err = schema.Views(obj.Flat(), schema.WithRegions(schema.AsRegion("eu")))
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Contains(t, findings[0].Title, `"us"`)
}
