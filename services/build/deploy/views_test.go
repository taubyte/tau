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
	"github.com/taubyte/tau/services/build/deploy"
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

// EACH REGION IS PUBLISHED ITS OWN VIEW, beside the whole object, and a deploy that
// changes nothing any region reads moves nothing.
func TestADeployPublishesOneViewPerRegion(t *testing.T) {
	r := registry(t)
	co := &checkout{dir: configDir(t, map[string]string{
		"project.yaml": viewProject,
		"cache/a.yaml": cacheDoc(cacheA, "regions:\n    pinned: [region-1]\n"),
		"cache/b.yaml": cacheDoc(cacheB, "regions:\n    pinned: [region-2]\n"),
	})}
	var log out
	require.NoError(t, runner(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2"))).Run(t.Context(), work(), &log))
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
	require.NoError(t, runner(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2"))).Run(t.Context(), work(), &log))
	require.Contains(t, log.String(), "unchanged at revision 1")

	// …and a cloud that serves one more region does, with an empty view for it.
	log = out{}
	require.NoError(t, runner(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2", "region-3"))).Run(t.Context(), work(), &log))
	require.Contains(t, log.String(), "published as revision 2")
	require.Contains(t, log.String(), "prod in region-3: nothing placed")
	require.Empty(t, heldIn(t, r, "region-3"))

	// A RESOURCE REMOVED IS GONE FROM ITS REGION'S VIEW, which is told so by an empty one.
	require.NoError(t, os.Remove(filepath.Join(co.dir, "cache/b.yaml")))
	log = out{}
	require.NoError(t, runner(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2", "region-3"))).Run(t.Context(), work(), &log))
	require.Contains(t, log.String(), "prod in region-2: nothing placed")
	require.Empty(t, heldIn(t, r, "region-2"))
	as, err = r.Assigned(t.Context(), "region-2")
	require.NoError(t, err)
	require.EqualValues(t, 3, as[0].View.Revision)
}

// AN EDGE ACROSS REGIONS IS REFUSED before anything publishes.
func TestADeployReachingAcrossRegionsIsRefused(t *testing.T) {
	r := registry(t)
	co := &checkout{dir: configDir(t, map[string]string{
		"project.yaml": viewProject,
		"cache/a.yaml": cacheDoc(cacheA, "regions:\n    pinned: [region-1]\n"),
		"function/f.yaml": "id: " + fnID + "\nsource: services/f\nbuild:\n    commands:\n        image: rust:1.83\n        run: cargo build\n        output: x.wasm\n" +
			"execution:\n    call: handle\n    timeout: 15s\n    memory: 32MiB\nregions:\n    pinned: [region-2]\ncache:\n    a:\n        ops: [get]\n",
	})}
	var log out
	err := runner(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2"))).Run(t.Context(), work(), &log)
	require.ErrorIs(t, err, deploy.ErrBroken)
	require.ErrorContains(t, err, `function/f: reaches cache/a, which is not placed in "region-2" where this is`)
	_, err = r.Resolve(t.Context(), shopID, "prod")
	require.ErrorIs(t, err, register.ErrNotPublished)
}

// A REGION THE CLOUD STOPS SERVING IS DROPPED from the deployment, though the object is
// the same: a new manifest that does not list it, so it stops resolving.
func TestADroppedRegionStopsResolving(t *testing.T) {
	r := registry(t)
	co := &checkout{dir: configDir(t, map[string]string{
		"project.yaml": viewProject,
		"cache/a.yaml": cacheDoc(cacheA, ""),
	})}
	var log out
	require.NoError(t, runner(t, co, r, deploy.WithRegions(schema.Preview("region-1", "region-2"))).Run(t.Context(), work(), &log))
	require.Equal(t, []string{cacheA}, heldIn(t, r, "region-2"))

	log = out{}
	require.NoError(t, runner(t, co, r, deploy.WithRegions(schema.Preview("region-1"))).Run(t.Context(), work(), &log))
	require.Contains(t, log.String(), "published as revision 2")
	require.Equal(t, []string{cacheA}, heldIn(t, r, "region-1"))
	_, err := r.ResolveView(t.Context(), shopID, "prod", "region-2")
	require.ErrorIs(t, err, register.ErrNoView)
	as, err := r.Assigned(t.Context(), "region-2")
	require.NoError(t, err)
	require.Empty(t, as)

	// A cloud serving no regions replaces region-1 with its one default region.
	log = out{}
	require.NoError(t, runner(t, co, r, deploy.WithRegions(schema.WithNoRegions())).Run(t.Context(), work(), &log))
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
	require.NoError(t, runner(t, co, r, regions).Run(t.Context(), work(), &out{}))
	require.ErrorContains(t, runner(t, co, manifestless{r}, regions).Run(t.Context(), work(), &out{}), "manifest unreadable")
}
