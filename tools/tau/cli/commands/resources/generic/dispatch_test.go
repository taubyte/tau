package generic

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/tcc"
	"gotest.tools/v3/assert"
)

func linkFor(t *testing.T, dir string) link {
	t.Helper()
	g, err := tcc.GroupFor(dir)
	assert.NilError(t, err)
	bind, err := New(g)
	assert.NilError(t, err)
	return bind().(link)
}

// NOTHING BELOW NAMES A KIND. The first version did — functions, websites,
// databases, applications — and every one of those directories was v1's plural
// spelling. They all broke together when the vocabulary went singular, and none
// of them was wrong about the CLI.
//
// So each shape is FOUND: the container is whichever kind declares itself one,
// the repo-backed one is whichever has a repository shape, and so on. A test
// that cannot find its shape says the vocabulary no longer has one, which is
// worth knowing on its own.

// byShape finds one kind of each shape the dispatcher branches on, and says
// which shapes this vocabulary has none of.
func byShape(t *testing.T) (code, repo, plain, container link) {
	t.Helper()

	groups, err := tcc.Groups()
	assert.NilError(t, err)

	var haveCode, havePlain, haveContainer bool
	_ = repo
	for _, g := range groups {
		f, err := tcc.FormFor(g.Def)
		assert.NilError(t, err)

		l := linkFor(t, g.Dir)
		switch {
		case g.Container && !haveContainer:
			container, haveContainer = l, true
		case f.Repo() != nil:
			repo = l
		case f.CodeBacked() && !haveCode:
			code, haveCode = l, true
		case !g.Container && !f.CodeBacked() && !havePlain:
			plain, havePlain = l, true
		}
	}

	assert.Assert(t, haveCode, "this vocabulary has no code-backed kind")
	assert.Assert(t, havePlain, "this vocabulary has no plain kind")
	assert.Assert(t, haveContainer, "this vocabulary has no container kind")
	return code, repo, plain, container
}

// TestNoKindCarriesItsOwnRepository, which is what v2 says and what the git
// verbs on this dispatcher were written for.
//
// A v1 resource named a GitHub repository of its own — provider, fullname, id,
// branch — and the CLI grew clone/push/pull/checkout/import per resource for
// it. v2 has ONE code repository per project and a resource names a path or a
// built component inside it, so `Form.Repo()` finds nothing anywhere and every
// one of those verbs is NotImplemented on every kind.
//
// THIS IS A TEST THAT SHOULD FAIL ONE DAY. It pins a real gap rather than
// hiding it: the repository flow here is unreachable code until either the
// vocabulary grows a per-resource repository again or the flow is deleted, and
// whichever happens should make this test wrong.
func TestNoKindCarriesItsOwnRepository(t *testing.T) {
	groups, err := tcc.Groups()
	assert.NilError(t, err)

	for _, g := range groups {
		f, err := tcc.FormFor(g.Def)
		assert.NilError(t, err)
		assert.Assert(t, f.Repo() == nil,
			"%s carries a repository shape; the git verbs are reachable again and this test is out of date", g.Dir)

		l := linkFor(t, g.Dir)
		for verb, cmd := range map[string]common.Command{
			"clone": l.Clone(), "push": l.Push(), "pull": l.Pull(),
			"checkout": l.Checkout(), "import": l.Import(),
		} {
			assert.Assert(t, cmd == common.NotImplemented, "%s exposes %s", g.Dir, verb)
		}
	}
}

// Which verbs a kind exposes follows its shape: a repo-backed kind gets the git
// verbs, a code-backed kind gets run, a container gets select/clear, and a plain
// kind gets none of those.
func TestVerbsByShape(t *testing.T) {
	code, _, plain, container := byShape(t)

	// run: code-backed only
	assert.Assert(t, code.Run() != common.NotImplemented)
	assert.Assert(t, plain.Run() == common.NotImplemented)

	// select/clear: container only
	assert.Assert(t, container.Select() != common.NotImplemented)
	assert.Assert(t, container.Clear() != common.NotImplemented)
	assert.Assert(t, code.Select() == common.NotImplemented)
	assert.Assert(t, plain.Clear() == common.NotImplemented)

	// new/edit/query/list/delete always present
	for _, l := range []link{code, plain, container} {
		assert.Assert(t, l.New() != nil)
		assert.Assert(t, l.Edit() != nil)
		assert.Assert(t, l.Query() != nil)
		assert.Assert(t, l.List() != nil)
		assert.Assert(t, l.Delete() != nil)
	}
}

// The base command carries the DSL name, the plural dir alias, and the CLI's
// established shorthand.
func TestBaseAliases(t *testing.T) {
	groups, err := tcc.Groups()
	assert.NilError(t, err)

	for _, g := range groups {
		cmd, _ := linkFor(t, g.Dir).Base()

		// THE COMMAND IS THE DSL'S NAME, and the directory is an alias so that
		// a plural anybody types still lands — which is the whole reason the
		// singular rename did not break the CLI itself.
		assert.Equal(t, cmd.Name, g.Name, g.Dir)
		if g.Dir != g.Name {
			assert.Assert(t, contains(cmd.Aliases, g.Dir),
				"%s is not an alias of %s", g.Dir, cmd.Name)
		}
		for _, alias := range cmd.Aliases {
			assert.Assert(t, alias != "", "%s has an empty alias", g.Dir)
		}
	}
}

// A repo-backed kind's flags include the repository flow's inputs and drop the
// per-field repository entries; a code-backed kind adds the template flags.
func TestFlagsByShape(t *testing.T) {
	names := func(l link) map[string]bool {
		m := map[string]bool{}
		for _, f := range l.flagsFor() {
			m[f.Names()[0]] = true
		}
		return m
	}
	codeLink, _, plainLink, _ := byShape(t)

	// A CODE-BACKED KIND OFFERS THE TEMPLATE FLAGS and no repository ones,
	// because no kind in this vocabulary has a repository of its own.
	code := names(codeLink)
	assert.Assert(t, code["template"])
	assert.Assert(t, !code["repository-name"])

	// A plain kind offers neither.
	plain := names(plainLink)
	assert.Assert(t, !plain["template"])
	assert.Assert(t, !plain["repository-name"])
}

// TestTheRepoAdapterHasNoShapeToReadThrough. It reads and writes a repository
// block through the DSL shape; no kind declares one, so there is nothing to
// build it over. Kept as a statement of that rather than deleted, so the day a
// shape comes back the adapter is remembered.
func TestTheRepoAdapterHasNoShapeToReadThrough(t *testing.T) {
	groups, err := tcc.Groups()
	assert.NilError(t, err)

	for _, g := range groups {
		f, err := tcc.FormFor(g.Def)
		assert.NilError(t, err)
		assert.Assert(t, f.Repo() == nil,
			"%s grew a repository shape; wire the adapter's test back up", g.Dir)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
