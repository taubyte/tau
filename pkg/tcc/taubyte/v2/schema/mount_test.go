package schema_test

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// mountedProject is a project whose docs site lives in its own repository, which
// carries the site's build the way a CI file lives with the code it builds.
func mountedProject(t *testing.T, siteBody string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\nname: shop\nregions: [region-1]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/package/docs.yaml",
		[]byte("id: QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL\ntype: assets\n"+siteBody), 0o644))
	return fs
}

func mountsOf(t *testing.T, fs afero.Fs, env string) []interp.Mount {
	t.Helper()
	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging")}
	if env != "" {
		opts = append(opts, schema.WithEnvironment(env))
	}
	c, err := schema.New(opts...)
	assert.NilError(t, err)
	mounts, err := c.Mounts(context.Background())
	assert.NilError(t, err)
	return mounts
}

func TestAProjectThatDelegatesNothingAsksForNoMounts(t *testing.T) {
	fs := mountedProject(t, "source:\n    git: https://github.com/acme/docs\n    branch: main\n")
	assert.Equal(t, len(mountsOf(t, fs, "production")), 0,
		"a source without `config` delegates nothing")
}

func TestTheCompilerReportsWhatItNeedsToFetch(t *testing.T) {
	fs := mountedProject(t, "source:\n"+
		"    git: https://github.com/acme/docs\n"+
		"    branch: main\n"+
		"    branch_staging: develop\n"+
		"    path: site\n"+
		"    config: taubyte.yaml\n")

	prod := mountsOf(t, fs, "production")
	assert.Equal(t, len(prod), 1)
	m := prod[0]
	assert.Equal(t, m.Kind, "package")
	assert.Equal(t, m.Name, "docs")
	assert.Equal(t, m.Repo, "https://github.com/acme/docs")
	assert.Equal(t, m.Branch, "main")
	assert.Equal(t, m.Path, "site")
	assert.Equal(t, m.Config, "taubyte.yaml")

	// AND THE BRANCH FOLLOWS THE ENVIRONMENT, which is the whole reason the request
	// carries one rather than leaving the caller to work it out: a staging deploy
	// that fetched production's branch would build the wrong commit and nothing
	// downstream could tell.
	staging := mountsOf(t, fs, "staging")
	assert.Equal(t, len(staging), 1)
	assert.Equal(t, staging[0].Branch, "develop")
}

func TestTheMountPointIsDerivedAndStable(t *testing.T) {
	fs := mountedProject(t, "source:\n"+
		"    git: https://github.com/acme/docs\n"+
		"    config: taubyte.yaml\n")

	first := mountsOf(t, fs, "production")
	second := mountsOf(t, fs, "production")
	assert.Equal(t, len(first), 1)
	assert.Equal(t, first[0].At, second[0].At, "the same project asks for the same place twice")
	assert.Equal(t, first[0].At, interp.MountPoint("", "package", "docs"))
}

// fulfil is the caller's half: it does what a build service would do with a Mount —
// fetch the repository somehow — and hands back the filesystem and the commit.
func fulfil(t *testing.T, m interp.Mount, commit string, files map[string]string) schema.Option {
	t.Helper()
	fs := afero.NewMemMapFs()
	for path, body := range files {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	return schema.WithMounted(m, fs, commit)
}

func compileMounted(t *testing.T, fs afero.Fs, env string, extra ...schema.Option) (map[string]any, error) {
	t.Helper()
	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging")}
	if env != "" {
		opts = append(opts, schema.WithEnvironment(env))
	}
	c, err := schema.New(append(opts, extra...)...)
	if err != nil {
		return nil, err
	}
	obj, _, err := c.Compile(context.Background())
	if err != nil {
		return nil, err
	}
	return only(t, resources(t, obj.Flat(), "package")), nil
}

const delegating = "source:\n" +
	"    git: https://github.com/acme/docs\n" +
	"    branch: main\n" +
	"    config: taubyte.yaml\n"

func TestABuildIsReadFromTheSourceRepository(t *testing.T) {
	fs := mountedProject(t, delegating)

	mounts := mountsOf(t, fs, "production")
	assert.Equal(t, len(mounts), 1)

	site, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{
			"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n",
		}))
	assert.NilError(t, err)

	assert.Equal(t, nested(site, "build", "kind"), "steps")
	step := nested(site, "build", "steps").([]any)[0].(map[string]any)
	assert.Equal(t, step["image"], "node:22")
}

func TestAValueSetInBothPlacesIsRefused(t *testing.T) {
	fs := mountedProject(t, delegating+"build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n")

	mounts := mountsOf(t, fs, "production")
	_, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{
			"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n",
		}))
	assert.ErrorContains(t, err, "only one may say it")
	assert.ErrorContains(t, err, "taubyte.yaml")
}

func TestAFillCannotWriteAnUndelegatedField(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	for _, body := range []string{
		"name: somethingelse\n",
		"id: QmPZVNdFnq1vBqWQ56rTHJkFUuA16uBpHUZSDzLToCVdVz\n",
		"source:\n    git: https://github.com/attacker/x\n",
		"cache:\n    sessions:\n        ops: [get]\n",
		"type: wasm\n",
		"output: elsewhere\n",
	} {
		_, err := compileMounted(t, fs, "production",
			fulfil(t, mounts[0], "def456", map[string]string{"taubyte.yaml": body}))
		assert.ErrorContains(t, err, "not delegated", "body %q", body)
	}
}

func TestAFillWithoutACommitIsRefused(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	_, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "", map[string]string{"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n"}))
	assert.ErrorContains(t, err, "provenance is not optional")
}

func auditMounted(t *testing.T, fs afero.Fs, env string, extra ...schema.Option) []schema.Finding {
	t.Helper()
	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging")}
	if env != "" {
		opts = append(opts, schema.WithEnvironment(env))
	}
	c, err := schema.New(append(opts, extra...)...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return schema.Audit(obj.Flat(), nil, schema.AuditIn(env))
}

func TestAMountThatWasNeverAnsweredIsReported(t *testing.T) {
	fs := mountedProject(t, delegating)

	found := auditMounted(t, fs, "production")
	has(t, found, schema.Check, "was not read from")
	hasNo(t, found, "broken")
}

func TestAMountThatWasAnsweredIsNotReported(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	found := auditMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{
			"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n",
		}))
	hasNo(t, found, "was not read from")
}

func TestTheObjectRecordsWhereTheFillCameFrom(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging"), schema.WithEnvironment("production"),
		fulfil(t, mounts[0], "def456", map[string]string{"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n"})}
	c, err := schema.New(opts...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	filled := interp.MountedOf(obj.Flat())
	assert.Equal(t, len(filled), 1)
	for _, f := range filled {
		assert.Equal(t, f.Commit, "def456")
		assert.Equal(t, f.Repo, "https://github.com/acme/docs")
		assert.Equal(t, f.Branch, "main")
		assert.Equal(t, f.Config, "taubyte.yaml")
		assert.Equal(t, f.Kind, "package")
		assert.Equal(t, f.Name, "docs")
	}
}

func TestAFillCannotCreateAnotherMount(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	answered := fulfil(t, mounts[0], "def456", map[string]string{
		// a fill trying to add a second delegating resource
		"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n",
	})

	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging"), schema.WithEnvironment("production"), answered}
	c, err := schema.New(opts...)
	assert.NilError(t, err)

	again, err := c.Mounts(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, len(again), 1, "pass two asks for exactly what pass one did, never more")
	assert.Equal(t, again[0].At, mounts[0].At)
}

func TestAFillIsReadUnderTheSourcePath(t *testing.T) {
	fs := mountedProject(t, "source:\n"+
		"    git: https://github.com/acme/monorepo\n"+
		"    path: sites/docs\n"+
		"    config: taubyte.yaml\n")

	mounts := mountsOf(t, fs, "production")
	site, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "abc123", map[string]string{
			"sites/docs/taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n",
			// the same name at the root must NOT be the one read
			"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: wrong:1\n          commands: [npm run build]\n",
		}))
	assert.NilError(t, err)
	step := nested(site, "build", "steps").([]any)[0].(map[string]any)
	assert.Equal(t, step["image"], "node:22", "the root file is not this resource's, so its value must not appear")
}

func TestAnAnsweredMountWithNoFileIsRefused(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	_, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{"something-else.yaml": "build:\n"}))
	assert.ErrorContains(t, err, "could not be read")
}

func TestAFillThatIsNotBlocksIsRefused(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	_, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{"taubyte.yaml": "- build\n- steps\n"}))
	assert.ErrorContains(t, err, "not readable as yaml")
}

func TestAFillThatSaysNothingStillRecordsItsCommit(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging"), schema.WithEnvironment("production"),
		fulfil(t, mounts[0], "def456", map[string]string{"taubyte.yaml": "# nothing yet\n"})}
	c, err := schema.New(opts...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	filled := interp.MountedOf(obj.Flat())
	assert.Equal(t, len(filled), 1)
	hasNo(t, schema.Audit(obj.Flat(), nil, schema.AuditIn("production")), "was not read from")
}

// asking records what the compiler asked for, and answers with a fixed tree. It is
// what a real caller is: a git checkout with credentials tcc does not have.
type asking struct {
	asked  []interp.Mount
	files  map[string]string
	commit string
	err    error
}

func (a *asking) Mount(_ context.Context, m interp.Mount) (afero.Fs, string, error) {
	a.asked = append(a.asked, m)
	if a.err != nil {
		return nil, "", a.err
	}
	fs := afero.NewMemMapFs()
	for path, body := range a.files {
		if err := afero.WriteFile(fs, path, []byte(body), 0o644); err != nil {
			return nil, "", err
		}
	}
	return fs, a.commit, nil
}

func TestAMounterMakesItOneCall(t *testing.T) {
	fs := mountedProject(t, delegating)
	ask := &asking{commit: "def456", files: map[string]string{
		"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n",
	}}

	site, err := compileMounted(t, fs, "production", schema.WithMounter(ask))
	assert.NilError(t, err)

	assert.Equal(t, nested(site, "build", "kind"), "steps")
	assert.DeepEqual(t, ask.asked, mountsOf(t, fs, "production"))
}

func TestTheMounterIsAskedOncePerMount(t *testing.T) {
	fs := mountedProject(t, delegating)
	ask := &asking{commit: "def456", files: map[string]string{"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n"}}

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging"), schema.WithEnvironment("production"),
		schema.WithMounter(ask))
	assert.NilError(t, err)
	for range 3 {
		_, _, err := c.Compile(context.Background())
		assert.NilError(t, err)
	}
	assert.Equal(t, len(ask.asked), 1)
}

func TestAMounterIsNotAskedWhenNothingDelegates(t *testing.T) {
	fs := mountedProject(t, "source:\n    git: https://github.com/acme/docs\n    branch: main\n")
	ask := &asking{commit: "def456"}

	_, err := compileMounted(t, fs, "production", schema.WithMounter(ask))
	assert.NilError(t, err)
	assert.Equal(t, len(ask.asked), 0)
}

func TestAMounterThatFailsFailsTheCompile(t *testing.T) {
	fs := mountedProject(t, delegating)
	ask := &asking{err: errors.New("no such ref")}

	_, err := compileMounted(t, fs, "production", schema.WithMounter(ask))
	assert.ErrorContains(t, err, "no such ref")
	assert.ErrorContains(t, err, "package/docs")
}

func TestAMountAnsweredByHandIsNotAsked(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")
	ask := &asking{commit: "def456", files: map[string]string{"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: wrong:1\n          commands: [npm run build]\n"}}

	site, err := compileMounted(t, fs, "production", schema.WithMounter(ask),
		fulfil(t, mounts[0], "abc123", map[string]string{"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n"}))
	assert.NilError(t, err)
	step := nested(site, "build", "steps").([]any)[0].(map[string]any)
	assert.Equal(t, step["image"], "node:22", "the hand-given answer is the one read")
	assert.Equal(t, len(ask.asked), 0)
}

func TestANilMounterIsRefused(t *testing.T) {
	_, err := schema.New(schema.WithVirtual(mountedProject(t, delegating), "/"),
		schema.WithBranch("main"), schema.WithMounter(nil))
	assert.ErrorContains(t, err, "nil mounter")
}
