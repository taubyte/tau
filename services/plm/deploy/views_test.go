package deploy_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/plm/deploy"
)

const (
	viewProject = "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: shop\nenvironments:\n    prod:\n        branches: [\"*\"]\n"
	cacheA      = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB"
	cacheB      = "QmVs4sQjPFqJZ7Ug5MPpFxPTNiZ1CT8xY4XPKUbnDNbNAY"
	fnID        = "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG"
)

func cacheDoc(id, block string) string {
	return "id: " + id + "\nsize: 1MB\nconsensus: crdt\n" + block
}

func configDir(t *testing.T, docs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, body := range docs {
		full := filepath.Join(dir, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	return dir
}

// heldIn is the ids of every cache the view a region reads holds.
func heldIn(t *testing.T, r *register.Register, region string) []string {
	t.Helper()
	v, err := r.ResolveView(t.Context(), shopID, "prod", region)
	require.NoError(t, err)
	body, err := r.Object(t.Context(), v.Object)
	require.NoError(t, err)
	var obj map[string]any
	require.NoError(t, json.Unmarshal(body, &obj))
	var out []string
	for _, res := range schema.Resources(obj) {
		out = append(out, res.ID)
	}
	return out
}

func TestADeployPublishesOneViewPerRegion(t *testing.T) {
	r := registry(t)
	co := &checkout{dir: configDir(t, map[string]string{
		"project.yaml":              viewProject,
		"namespace/eu/meta.yaml":    "id: QmTXGKe63GYuKA54yerPQmGyEtFjhUF5p1g2hahTpm6pkA\nregions:\n    in: [region-1]\n",
		"namespace/us/meta.yaml":    "id: QmWXdfF6xBdc4pVfpHf6Gf3aaPZh7tb3fbRG9vNukpZXxc\nregions:\n    in: [region-2]\n",
		"namespace/eu/cache/a.yaml": cacheDoc(cacheA, ""),
		"namespace/us/cache/b.yaml": cacheDoc(cacheB, ""),
	})}
	var log out
	require.NoError(t, pushed(t.Context(), builder(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2"))), work(), &log))
	require.Contains(t, log.String(), "prod in region-1: 1 resources")
	require.Equal(t, []string{cacheA}, heldIn(t, r, "region-1"))
	require.Equal(t, []string{cacheB}, heldIn(t, r, "region-2"))
	_, err := r.ResolveView(t.Context(), shopID, "prod", "region-3")
	require.ErrorIs(t, err, register.ErrNoView)

	as, err := r.Assigned(t.Context(), "region-2")
	require.NoError(t, err)
	require.Len(t, as, 1)

	// THE SAME PUSH AGAIN publishes nothing…
	log = out{}
	require.NoError(t, pushed(t.Context(), builder(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2"))), work(), &log))
	require.Contains(t, log.String(), "unchanged at revision 1")

	// …and a cloud that serves one more region does, with an empty view for it.
	log = out{}
	require.NoError(t, pushed(t.Context(), builder(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2", "region-3"))), work(), &log))
	require.Contains(t, log.String(), "published as revision 2")
	require.Contains(t, log.String(), "prod in region-3: nothing placed")
	require.Empty(t, heldIn(t, r, "region-3"))

	// A RESOURCE REMOVED IS GONE FROM ITS REGION'S VIEW, which is told so by an empty one.
	require.NoError(t, os.Remove(filepath.Join(co.dir, "namespace/us/cache/b.yaml")))
	log = out{}
	require.NoError(t, pushed(t.Context(), builder(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2", "region-3"))), work(), &log))
	require.Contains(t, log.String(), "prod in region-2: nothing placed")
	require.Empty(t, heldIn(t, r, "region-2"))
	as, err = r.Assigned(t.Context(), "region-2")
	require.NoError(t, err)
	require.EqualValues(t, 3, as[0].View.Revision)
}

func TestADeployReachingAcrossRegionsIsRefused(t *testing.T) {
	r := registry(t)
	co := &checkout{dir: configDir(t, map[string]string{
		"project.yaml":              viewProject,
		"namespace/eu/meta.yaml":    "id: QmTXGKe63GYuKA54yerPQmGyEtFjhUF5p1g2hahTpm6pkA\nregions:\n    in: [region-1]\n",
		"namespace/us/meta.yaml":    "id: QmWXdfF6xBdc4pVfpHf6Gf3aaPZh7tb3fbRG9vNukpZXxc\nregions:\n    in: [region-2]\n",
		"namespace/eu/cache/a.yaml": cacheDoc(cacheA, ""),
		"package/f_code.yaml":       "id: QmP66dBkc5zUMF7W7HuU3HgTmPa1w92gGNG4zY854f4Wkt\ntype: wasm\n",
		"namespace/us/function/f.yaml": "id: " + fnID + "\npackage:\n    name: f_code\n" +
			"execution:\n    call: handle\n    timeout: 15s\n    memory: 32MiB\ncache:\n    a:\n        namespace: /eu\n        ops: [get]\n",
	})}
	var log out
	err := pushed(t.Context(), builder(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2"))), work(), &log)
	require.ErrorIs(t, err, deploy.ErrBroken)
	require.ErrorContains(t, err, `function/f in us: reaches cache/a, which does not run in "region-2" where this does`)
	_, err = r.Resolve(t.Context(), shopID, "prod")
	require.ErrorIs(t, err, register.ErrNotPublished)
}

func TestADroppedRegionStopsResolving(t *testing.T) {
	r := registry(t)
	co := &checkout{dir: configDir(t, map[string]string{
		"project.yaml": viewProject,
		"cache/a.yaml": cacheDoc(cacheA, ""),
	})}
	var log out
	require.NoError(t, pushed(t.Context(), builder(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2"))), work(), &log))
	require.Equal(t, []string{cacheA}, heldIn(t, r, "region-2"))

	log = out{}
	require.NoError(t, pushed(t.Context(), builder(t, co, r, deploy.WithRegions(schema.Preview("region-1"))), work(), &log))
	require.Contains(t, log.String(), "published as revision 2")
	require.Equal(t, []string{cacheA}, heldIn(t, r, "region-1"))
	_, err := r.ResolveView(t.Context(), shopID, "prod", "region-2")
	require.ErrorIs(t, err, register.ErrNoView)
	as, err := r.Assigned(t.Context(), "region-2")
	require.NoError(t, err)
	require.Empty(t, as)

	// A cloud serving no regions replaces region-1 with its one default region.
	log = out{}
	require.NoError(t, pushed(t.Context(), builder(t, co, r, deploy.WithRegions(schema.WithNoRegions())), work(), &log))
	require.Contains(t, log.String(), "published as revision 3")
	m, _, err := r.Manifest(t.Context(), shopID, "prod")
	require.NoError(t, err)
	require.Len(t, m.Views, 1)
	require.Contains(t, m.Views, schema.DefaultRegion)
}

type manifestless struct{ *register.Register }

func (manifestless) Manifest(context.Context, string, string) (register.Manifest, register.ObjectID, error) {
	return register.Manifest{}, "", errors.New("manifest unreadable")
}

func TestAnUnreadableManifestFailsTheDeploy(t *testing.T) {
	r := registry(t)
	co := &checkout{dir: configDir(t, map[string]string{
		"project.yaml": viewProject,
		"cache/a.yaml": cacheDoc(cacheA, ""),
	})}
	regions := deploy.WithRegions(schema.Preview("region-1"))
	require.NoError(t, pushed(t.Context(), builder(t, co, r, regions), work(), &out{}))
	require.ErrorContains(t, pushed(t.Context(), builder(t, co, manifestless{r}, regions), work(), &out{}), "manifest unreadable")
}

// edge is the corpus's edge project as the shop: a route answered in both regions,
// in front of a function in /origin that runs in region-1 alone.
func edge(t *testing.T) string {
	t.Helper()
	dir := fixture(t, filepath.Join("..", "..", "..", "instance", "dream", "projects", "with", "regions", "edge"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.yaml"), []byte("id: "+shopID+"\nname: shop\n"+
		"regions:\n    topology: active\n    in: [region-1, region-2]\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	return dir
}

func TestARegionIsToldWhereARouteReachesWhatItDoesNotRun(t *testing.T) {
	for _, regions := range []schema.Regions{schema.Preview("region-1", "region-2"), schema.WithRegion("region-2")} {
		r := registry(t)
		var log out
		require.NoError(t, pushed(t.Context(), builder(t, &checkout{dir: edge(t)}, r, deploy.WithRegions(regions)), work(), &log), log.String())

		two, err := r.Routing(t.Context(), shopID, "prod", "region-2")
		require.NoError(t, err)
		require.Equal(t, []register.Remote{{Kind: "function", Scope: "origin", Name: "api", In: []string{"region-1"}}}, two.Remote)
		whole, err := r.Resolve(t.Context(), shopID, "prod")
		require.NoError(t, err)
		require.Equal(t, whole.Object, two.Release, "the release is the whole object")
	}

	r := registry(t)
	var log out
	require.NoError(t, pushed(t.Context(), builder(t, &checkout{dir: edge(t)}, r, deploy.WithRegions(schema.WithRegion("region-1"))), work(), &log))
	one, err := r.Routing(t.Context(), shopID, "prod", "region-1")
	require.NoError(t, err)
	require.Empty(t, one.Remote, "region-1 runs what its routes reach")
}
