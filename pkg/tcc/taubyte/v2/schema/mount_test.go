package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// mountedProject is a project whose docs site lives in its own repository.
func mountedProject(t *testing.T, siteBody string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: shop\nregions: [eu]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/website/docs.yaml",
		[]byte("id: QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL\n"+siteBody), 0o644))
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

// TestAProjectThatDelegatesNothingNeedsNothing.
//
// The common case has to cost nothing: a project whose content is all in its own
// repository asks for no mounts, so nothing about the compile changes and no caller
// has to learn about any of this.
func TestAProjectThatDelegatesNothingAsksForNoMounts(t *testing.T) {
	fs := mountedProject(t, "source:\n    git: https://github.com/acme/docs\n    branch: main\n")
	assert.Equal(t, len(mountsOf(t, fs, "production")), 0,
		"a source without `config` delegates nothing")
}

// TestWhatTheCompilerSaysItNeeds — the whole of pass one.
//
// tcc cannot fetch: no network, no credentials, no git, deliberately. So it reports
// the request and the caller answers it. Everything the caller needs to do the
// checkout is in the request, INCLUDING THE BRANCH ALREADY RESOLVED for this
// environment, so no two callers can resolve it differently.
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
	assert.Equal(t, m.Kind, "website")
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

// TestTheMountPointIsTheCompilersAndNotTheCallers.
//
// It becomes the path an error is reported against, so two callers of one project
// have to produce the same message. A caller that chose it would make the same
// broken fill read differently from `tau` than from CI.
func TestTheMountPointIsDerivedAndStable(t *testing.T) {
	fs := mountedProject(t, "source:\n"+
		"    git: https://github.com/acme/docs\n"+
		"    config: taubyte.yaml\n")

	first := mountsOf(t, fs, "production")
	second := mountsOf(t, fs, "production")
	assert.Equal(t, len(first), 1)
	assert.Equal(t, first[0].At, second[0].At, "the same project asks for the same place twice")
	assert.Equal(t, first[0].At, interp.MountPoint("", "website", "docs"))
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
	return only(t, resources(t, obj.Flat(), "website")), nil
}

const delegating = "source:\n" +
	"    git: https://github.com/acme/docs\n" +
	"    branch: main\n" +
	"    config: taubyte.yaml\n"

// TestTheWholeLoop — pass one says what it needs, the caller answers, pass two
// compiles one object with the answer in it.
//
// The serving block was never written in the configuration repository; it is read
// from the repository whose build decides it, and the compiled object cannot tell
// the difference. That is the point: everything downstream — references, lowering,
// the audit — sees one tree.
func TestAServingBlockIsReadFromTheSourceRepository(t *testing.T) {
	fs := mountedProject(t, delegating)

	mounts := mountsOf(t, fs, "production")
	assert.Equal(t, len(mounts), 1)

	site, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{
			"taubyte.yaml": "serve:\n    mode: spa\n    index: app.html\n",
		}))
	assert.NilError(t, err)

	assert.Equal(t, nested(site, "serve", "mode"), "spa")
	assert.Equal(t, nested(site, "serve", "index"), "app.html")
}

// TestAFieldWithTwoAuthorsIsRefused.
//
// It is the same defect as a switch with two branches open: there is no rule for
// which wins that is not a surprise to one of them. So both-set is refused naming
// both, rather than one silently beating the other.
func TestAValueSetInBothPlacesIsRefused(t *testing.T) {
	fs := mountedProject(t, delegating+"serve:\n    mode: static\n")

	mounts := mountsOf(t, fs, "production")
	_, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{
			"taubyte.yaml": "serve:\n    mode: spa\n",
		}))
	assert.ErrorContains(t, err, "only one may say it")
	assert.ErrorContains(t, err, "taubyte.yaml")
}

// TestAFillCannotSayWhatTheSchemaDidNotDelegate.
//
// WHAT may be delegated is the schema's answer, not the author's: a resource says
// only WHETHER it delegates. A fill that sets anything else is refused by name —
// ignoring it would let the fill believe it had set something.
func TestAFillCannotWriteAnUndelegatedField(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	for _, body := range []string{
		"name: somethingelse\n",
		"id: QmPZVNdFnq1vBqWQ56rTHJkFUuA16uBpHUZSDzLToCVdVz\n",
		"source:\n    git: https://github.com/attacker/x\n",
		"cache:\n    sessions:\n        ops: [get]\n",
	} {
		_, err := compileMounted(t, fs, "production",
			fulfil(t, mounts[0], "def456", map[string]string{"taubyte.yaml": body}))
		assert.ErrorContains(t, err, "not delegated", "body %q", body)
	}
}

// TestACommitIsNotOptional — a branch name says what was asked for; a commit says
// what was read. A caller that cannot say which commit it fetched has not fetched
// reproducibly, and recording a blank would make provenance a claim.
func TestAFillWithoutACommitIsRefused(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	_, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "", map[string]string{"taubyte.yaml": "serve:\n    mode: spa\n"}))
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

// TestAnUnreadFillIsSaidOutLoud.
//
// The compile is legitimate — it produced the configuration repository's own values,
// which is what the project got before it delegated anything, and a first deploy has
// no commit to read at. But "first deploy, using the defaults" and "the fetch has
// been broken for six weeks" produce the SAME object, so the difference is reported.
//
// This is the one place that must not behave like Resolver, whose stub says yes on
// purpose: an unwired caller here would otherwise get a green compile that quietly
// dropped somebody's serving rules.
func TestAMountThatWasNeverAnsweredIsReported(t *testing.T) {
	fs := mountedProject(t, delegating)

	found := auditMounted(t, fs, "production")
	has(t, found, schema.Check, "was not read from")
	hasNo(t, found, "broken")
}

// TestAnAnsweredFillIsNotReported — the other half, so the finding means something.
func TestAMountThatWasAnsweredIsNotReported(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	found := auditMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{
			"taubyte.yaml": "serve:\n    mode: spa\n",
		}))
	hasNo(t, found, "was not read from")
}

// TestTheObjectSaysWhichCommitItIsServing.
//
// A consumer answers "where did this come from" from the object alone, with no
// second index and nothing to re-run. That is what keeps the whole visible when part
// of it was written somewhere else: the authored config is partial, the PUBLISHED
// object is not.
func TestTheObjectRecordsWhereTheFillCameFrom(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging"), schema.WithEnvironment("production"),
		fulfil(t, mounts[0], "def456", map[string]string{"taubyte.yaml": "serve:\n    mode: spa\n"})}
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
		assert.Equal(t, f.Kind, "website")
		assert.Equal(t, f.Name, "docs")
	}
}

// TestTwoPassesAndNeverThree.
//
// A fill may not mint a resource, so pass two cannot discover a mount that pass one
// did not already ask for. That is not a rule checked afterwards — it falls out of
// the walk reading the AUTHORED object, which is complete before any fill exists. If
// it did not hold, the protocol would be "fetch until it stops changing", which has
// no bound and no reproducibility.
func TestAFillCannotCreateAnotherMount(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	answered := fulfil(t, mounts[0], "def456", map[string]string{
		// a fill trying to add a second delegating resource
		"taubyte.yaml": "serve:\n    mode: spa\n",
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

// TestTheFillIsReadRelativeToTheContentPath — a repository whose site is in a
// subdirectory carries its config there too, beside the thing it describes.
func TestAFillIsReadUnderTheSourcePath(t *testing.T) {
	fs := mountedProject(t, "source:\n"+
		"    git: https://github.com/acme/monorepo\n"+
		"    path: sites/docs\n"+
		"    config: taubyte.yaml\n")

	mounts := mountsOf(t, fs, "production")
	site, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "abc123", map[string]string{
			"sites/docs/taubyte.yaml": "serve:\n    mode: spa\n",
			// the same name at the root must NOT be the one read
			"taubyte.yaml": "serve:\n    index: wrong.html\n",
		}))
	assert.NilError(t, err)
	assert.Equal(t, nested(site, "serve", "mode"), "spa")
	assert.Equal(t, nested(site, "serve", "index"), "index.html",
		"the root file is not this resource's, so its value must not appear")
}

// TestAFillThatIsNotThereIsRefusedAndNotIgnored.
//
// A caller that answered a mount has claimed to have fetched it. If the file it
// promised is absent, serving the configuration repository's values instead would be
// the silent drop the whole design exists to prevent — the unanswered case is a
// finding, but a WRONGLY answered one is an error.
func TestAnAnsweredMountWithNoFileIsRefused(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	_, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{"something-else.yaml": "serve:\n"}))
	assert.ErrorContains(t, err, "could not be read")
}

// TestAFillThatIsNotAMappingIsRefused — a list or a scalar is not a set of blocks,
// and guessing what was meant is how a fill half-applies.
func TestAFillThatIsNotBlocksIsRefused(t *testing.T) {
	fs := mountedProject(t, delegating)
	mounts := mountsOf(t, fs, "production")

	_, err := compileMounted(t, fs, "production",
		fulfil(t, mounts[0], "def456", map[string]string{"taubyte.yaml": "- serve\n- mode\n"}))
	assert.ErrorContains(t, err, "not readable as yaml")
}

// TestAnEmptyFillIsNotAnError — a repository that has the file and says nothing in it
// yet is a repository mid-adoption, not a broken one. It compiles, and the record
// still says which commit was read, so the next question ("why is nothing applied")
// is answerable.
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
