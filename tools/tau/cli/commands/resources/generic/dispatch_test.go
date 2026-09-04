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

// TestRepositoryBackedKindsAreFound. A resource's source can be another
// repository — `source: {git: <clone URL>, branch: …, path: …}` — and the CLI's
// git verbs are for exactly those kinds.
//
// THIS FAILED FOR A WHILE AND THE READING WAS WRONG, not the vocabulary. The
// shape looked for v1's `source/{github}/fullname` plus a numeric id, found it
// nowhere, and the conclusion drawn was that v2 had dropped per-resource
// repositories. It had renamed how one is written: the URL is the provider,
// which is what makes a second forge a value rather than a schema edit.
func TestRepositoryBackedKindsAreFound(t *testing.T) {
	groups, err := tcc.Groups()
	assert.NilError(t, err)

	found := 0
	for _, g := range groups {
		f, err := tcc.FormFor(g.Def)
		assert.NilError(t, err)

		repo := f.Repo()
		if repo == nil {
			continue
		}
		found++

		assert.DeepEqual(t, repo.URL, []string{"source", "git"})
		assert.DeepEqual(t, repo.Branch, []string{"source", "branch"})
		assert.DeepEqual(t, repo.Path, []string{"source", "path"})
		assert.Assert(t, contains(repo.Kind.Alternatives, "git"),
			"%s carries a git URL and its source switch cannot open it", g.Dir)

		l := linkFor(t, g.Dir)
		for verb, cmd := range map[string]common.Command{
			"clone": l.Clone(), "push": l.Push(), "pull": l.Pull(),
			"checkout": l.Checkout(), "import": l.Import(),
		} {
			assert.Assert(t, cmd != common.NotImplemented, "%s exposes no %s", g.Dir, verb)
		}
	}

	assert.Assert(t, found > 0, "no kind in this vocabulary can name a repository")
}

// TestAKindWithNoRepositoryHasNoGitVerbs.
func TestAKindWithNoRepositoryHasNoGitVerbs(t *testing.T) {
	groups, err := tcc.Groups()
	assert.NilError(t, err)

	checked := 0
	for _, g := range groups {
		f, err := tcc.FormFor(g.Def)
		assert.NilError(t, err)
		if f.Repo() != nil {
			continue
		}
		checked++

		l := linkFor(t, g.Dir)
		for verb, cmd := range map[string]common.Command{
			"clone": l.Clone(), "push": l.Push(), "pull": l.Pull(),
			"checkout": l.Checkout(), "import": l.Import(),
		} {
			assert.Assert(t, cmd == common.NotImplemented, "%s exposes %s", g.Dir, verb)
		}
	}

	assert.Assert(t, checked > 0)
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

// TestTheRepoAdapterReadsAndWritesTheCloneURL. The driver speaks in a full name
// and an id because that is what the GitHub API answers with; the document
// carries a URL, so the adapter parses one into the other and drops the id —
// what the platform indexes a repository by is its canonical key
// (pkg/git/repokey), not a number one forge assigns.
func TestTheRepoAdapterReadsTheCloneURL(t *testing.T) {
	_, repo, _, _ := byShape(t)

	r := &resource{
		l:     repo,
		shape: repo.repo,
		name:  "site",
		doc: tcc.Doc{
			"description": "d",
			"source": map[string]any{
				"git":    "https://gitlab.com/acme/prompts",
				"branch": "main",
			},
		},
	}

	g := r.Get()
	assert.Equal(t, g.Name(), "site")
	assert.Equal(t, g.Description(), "d")
	assert.Equal(t, g.RepositoryURL(), "https://gitlab.com/acme/prompts")
	assert.Equal(t, g.RepoName(), "acme/prompts")
	assert.Equal(t, g.Branch(), "main")
	assert.Equal(t, g.RepoID(), "", "there is no id in v2, and never will be")

	// A full name from the picker becomes a URL; a URL is kept as it is.
	r.Set().RepoName("acme/other")
	assert.Equal(t, r.Get().RepositoryURL(), "https://github.com/acme/other")

	r.Set().RepoName("ssh://git@gitea.example.com/acme/third")
	assert.Equal(t, r.Get().RepositoryURL(), "ssh://git@gitea.example.com/acme/third")
	assert.Equal(t, r.Get().RepoName(), "acme/third")

	r.Set().RepoName("")
	assert.Equal(t, r.Get().RepositoryURL(), "")
	assert.Equal(t, r.Get().RepoName(), "")
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
