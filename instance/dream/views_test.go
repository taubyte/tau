//go:build dreaming

package dream_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	storageService "github.com/taubyte/tau/services/storage"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// projects/with/views and projects/refused/views: a project cut into one view per
// region (schema.Views), each node reading its own (its Config.Region). region-1
// answers the route; region-2 holds the gateway, cache and zone, and refuses the
// route by name, since its handler runs in region-1 alone.
var twoRegions = schema.Preview("region-1", "region-2")

// inRegion starts a dream cloud standing for one region of region-1 and region-2, and
// publishes the project into it as that region: its own view, and only that.
func inRegion(t *testing.T, region, name string) (*running, *nodeDream.Cloud) {
	t.Helper()
	return inRegionAt(t, region, name, "regional.example.com")
}

// inRegionAt is inRegion for a project answering on its own host.
func inRegionAt(t *testing.T, region, name, host string) (*running, *nodeDream.Cloud) {
	t.Helper()
	u, err := nodeDream.Start(t.Context(), t.Name()+"-"+region, nodeDream.Config{
		Services: []string{
			instance.Register, instance.Worker, instance.Stats, instance.Gateway, instance.Database, instance.Primitives,
			storageService.Descriptor.Name, instance.Builder, instance.Auth,
		},
		Configure: func(_ int, c *instance.Config) { c.Region = region },
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	r := publish(t, u, u.Pids()[:1], deployed{name: name, host: host, regions: schema.WithRegion(region)}, artifacts{"": handlerWASM(t)})[0]
	return r, u
}

// viewHeld is what a region's view of the shop deployment holds, by kind/name.
func viewHeld(t *testing.T, r *running, region string) (map[string]bool, uint64) {
	t.Helper()
	n := r.node
	v, err := n.Register().Local().ResolveView(t.Context(), r.project, "prod", region)
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

	r1, _ := inRegion(t, "region-1", "with/views")
	held, _ := viewHeld(t, r1, "region-1")
	require.Equal(t, map[string]bool{"function/hello": true, "package/hello_code": true, "gateway/regional": true, "zone/example_com": true, "cache/sessions": true}, held)

	var said string
	require.Eventually(t, func() bool {
		got := r1.ask(t, http.MethodGet, "/")
		said = got.Body.String()
		return got.Code == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond, "region-1 answers the route it holds: %s", &lastSaid{&said})

	// region-2 HOLDS WHAT RUNS ANYWHERE, and nothing that runs in region-1 alone: the
	// gateway runs with its function in region-1, so region-2 serves no such name.
	r2, u2 := inRegion(t, "region-2", "with/views")
	r2Node := r2.node
	held, _ = viewHeld(t, r2, "region-2")
	require.Equal(t, map[string]bool{"cache/sessions": true}, held)
	require.Equal(t, http.StatusNotFound, r2.ask(t, http.MethodGet, "/").Code, "region-2 serves no gateway at that name")

	as, err := r2Node.Register().Local().Assigned(t.Context(), "region-2")
	require.NoError(t, err)
	require.Len(t, as, 1, "region-2 learns of the deployment from its own index")

	// A RESOURCE REMOVED IN A LATER REVISION LEAVES ITS REGION'S VIEW: the cache goes,
	// region-2 still holds the deployment, at the new revision, with nothing in it.
	later := t.TempDir()
	require.NoError(t, os.CopyFS(later, os.DirFS(projectDir("with/views"))))
	require.NoError(t, os.Remove(filepath.Join(later, "cache", "sessions.yaml")))
	_, _, built := projectAs(t, later, artifacts{"": handlerWASM(t)}, schema.WithRegion("region-2"))
	build(t, u2, r2.project, later, built)
	held, revision := viewHeld(t, r2, "region-2")
	require.EqualValues(t, 2, revision)
	require.Empty(t, held)
}

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
	require.Equal(t, []string{`function/reader: reaches cache/sessions_r1, which does not run in "region-2" where this does`}, broken)

	// AN EDGE IS JUDGED IN ONE REGION'S VIEW: as region-1, the reader in region-2 is not in it,
	// so nothing region-1 holds reaches across; as region-2, the reader reaches region-1's cache.
	views, _, err := schema.Views(obj.Flat(), schema.WithRegions(schema.WithRegion("region-1")))
	require.NoError(t, err)
	for _, res := range schema.Resources(views["region-1"].Object) {
		require.NotEqual(t, "function/reader", res.Kind+"/"+res.Name)
	}
	_, _, err = schema.Views(obj.Flat(), schema.WithRegions(schema.WithRegion("region-2")))
	require.ErrorIs(t, err, schema.ErrUnviewable)
}
