package schema

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// WHERE A RESOURCE'S CONTENT COMES FROM — one switch, and it is OPTIONAL.
//
// There used to be two blocks both called `source`. A workload's said which of the
// project's own artifacts to build; a component's and a website's said which GitHub
// repository to clone. Same key, different shapes, different kinds, and neither could
// say the thing a monorepo makes ordinary: "in this repository, over there".
//
// Now it is one Either, and the branch names what kind of source it is:
//
//	(nothing)                     this repository, at the resource's own location
//	source: agents/support        this repository, at that path
//	source: {component: paylib}   another component of this project (+ `namespace`)
//	source: {builtin: agent}      a program the platform ships; a lowering sets it
//	source: {git: <url>, branch: …, path: …}   another repository, by its clone URL
//	source: {image: <ref>}        an artifact somebody else already built
//
// AN IMAGE IS A SOURCE, NOT A BUILD, and it took a `build:` block to notice. An OCI
// reference answers exactly the question this helper was written for — where does this
// resource's content come from — and the answer is "from somebody else, finished". It
// used to be a top-level `image:` beside `source:` on three kinds, each with its own
// copy of the pull secret, which re-created precisely the defect this switch was made
// to remove: two blocks both saying where the content is, with nothing stopping a
// document from writing both. Here they are branches of one Either and the pair is
// unrepresentable.
//
// THE COMMON CASE IS A BARE PATH. A switch whose default branch takes one argument
// should not make an author write the branch's name to give it: `source: agents/support`
// is the value at the switch's own key, and the block form is what the other three
// need because they carry more than a path.
//
// Absent is the common case and writes nothing, which is what an optional Either
// costs: the switch only errors when the block is there and names something it does
// not have.
//
// THE URL IS THE PROVIDER. `github:` as a branch key closed the vocabulary to one
// forge and made a second one a schema edit; a clone URL carries the host, so GitLab,
// Gitea, Codeberg and an internal mirror are all just values. The numeric repo id
// went with it — it is a GitHub fact, not a git one, and what the platform needs to
// index a repository it can resolve from the URL at publish.
func contentSource(section string, branches ...string) []*engine.Attribute {
	attrs := []*engine.Attribute{
		// The bare path, and the switch that reads the block forms. They share the
		// key: a scalar there is this path, a mapping is one of the branches.
		String("source", IsRepoLocation(), InSection(section),
			Doc("Source", "Where in this repository the content is, e.g. \"agents/support\". \".\" is the repository root. Omitted means the resource's own location.")),
		String(Key("source", "kind"), Path("source", Either(branches...)), Captures(), Field("Kind"), InSection(section),
			Doc("Source Kind", "Where the content comes from when it is not this repository.")),
	}
	for _, b := range branches {
		switch b {
		case "component":
			attrs = append(attrs,
				String(Key("source", "component"), Path("source", "component"), IsVariableName(), Ref("component"), InSection(section),
					Doc("Component", "A component of this project to build from.")),
				String(Key("source", "namespace"), IsNamespacePath(), InSection(section), ShowWhen(Key("source", "kind"), "component"),
					Doc("Component Namespace", "Where that component lives. Omitted means this resource's own scope, then its parents.")))
		case "image":
			attrs = append(attrs,
				// PerEnv: a preview deployment running a different tag is the same
				// resource with one line beside the other, which is what every other
				// ceiling and image reference here already does.
				String(Key("source", "image"), Path("source", "image"), PerEnv(), InSection(section),
					Doc("Image", "OCI image reference this runs. Somebody else built it, so there is nothing here to build.")),
				// The credential is a SECRET named, never a token held — the reason a
				// database's credentials are too. And it lives inside this branch
				// because this is the only branch it means anything in: it hung off
				// three kinds beside a top-level `image:`, and on one of them — the
				// worker, whose image was demanded only when its runtime was the
				// container tier — it could be written on a wasm workload that pulls
				// nothing at all.
				String(Key("source", "image_pull_secret", "namespace"), IsNamespacePath(), InSection(section),
					ShowWhen(Key("source", "kind"), "image"),
					Doc("Image Pull Secret Namespace", "Where that secret lives. Omitted means this resource's own scope, then its parents.")),
				String(Key("source", "image_pull_secret", "name"), IsVariableName(), Ref("secret", Inbound()), InSection(section),
					ShowWhen(Key("source", "kind"), "image"),
					Doc("Image Pull Secret", "Secret holding the registry credential this image is pulled with. Omit for a public registry.")))
		case "builtin":
			attrs = append(attrs, String(Key("source", "builtin"), Path("source", "builtin"), IsVariableName(), InSection(section),
				Doc("Built-in Program", "A program the platform itself ships, by name. Not for authors: a lowering sets it, and the set is closed by what exists.")))
		case "git":
			attrs = append(attrs,
				String(Key("source", "git"), Path("source", "git"), IsGitURL(), InSection(section),
					Doc("Repository", "Clone URL of the repository, e.g. \"https://gitlab.com/acme/prompts\". The host is the provider; nothing else names it.")),
				String(Key("source", "branch"), RepoBranch(), InSection(section), ShowWhen(Key("source", "kind"), "git"),
					Doc("Branch", "Branch to read. Omitted means the repository's default branch.")),
				String(Key("source", "path"), IsRepoPath(), InSection(section), ShowWhen(Key("source", "kind"), "git"),
					Doc("Path", "Where in that repository the content is. Omitted means its root.")))
		default:
			panic("contentSource: unknown branch " + b)
		}
	}
	return attrs
}

// repoPathSegment is a segment holding at least one character that is not a dot, so
// `.`, `..` and `...` are all outside it. That is what excludes both a path that
// climbs out of the repository and a path that names the repository root a second
// way — `source: "."` was a third spelling beside "absent", and it used to compile.
const repoPathSegment = `[A-Za-z0-9._-]*[A-Za-z0-9_-][A-Za-z0-9._-]*`

// repoPathRoot is the repository root, and it is the one dot-only path that means
// something.
//
// WHETHER A FIELD MAY NAME IT IS THE FIELD'S OWN QUESTION, and there is one test: what
// does leaving the field OUT mean? Where absent already names the root — `source.git.path`
// says "omitted means that repository's root" — `.` is a second spelling of a state the
// document can already reach, and that is what F29 refused. Where absent names something
// ELSE, the root is a place with no other spelling, and refusing it removes a state the
// vocabulary could express and leaves nothing in its place.
//
// Two fields are the second case, and they are unlike each other, which is why this is a
// test and not a roster: the bare `source`, where absent is the resource's OWN location
// (`LayoutOf().Content`, the directory beside its document), and `build/output`, where
// absent is the output directory the BUILDER hands the build and is not in the source
// tree at all. Ask it of every field that takes a path.
const repoPathRoot = "."

// repoPathPattern is the grammar, and repoPathChecker enforces exactly it. They were
// written to "mirror" each other and did not: the pattern accepted `a/../b` while the
// checker refused it, and the checker accepted `café/x` and a whole English sentence
// while the pattern refused them. An editor validating against one and a compiler
// enforcing the other disagree about the same document, which is the failure the
// introspectable pattern exists to prevent — so there is now one grammar, and the
// checker's only job is to say WHICH rule was broken.
const repoPathPattern = `^` + repoPathSegment + `(/` + repoPathSegment + `)*$`

// repoLocationPattern is the same grammar plus the root, and a field takes it exactly
// when the test above says so: it names a DIRECTORY, and absent means somewhere other
// than the root. A field naming a FILE never takes it, whatever absent means there,
// because the root is not a file — `build/script`, `build/dockerfile` and an agent's
// `instructions` are all paths to something to open. Two grammars rather than one,
// because one grammar put the root everywhere the moment it was legal anywhere.
const repoLocationPattern = `^(\.|` + repoPathSegment + `(/` + repoPathSegment + `)*)$`

var (
	repoPathRegex     = regexp.MustCompile(repoPathPattern)
	repoLocationRegex = regexp.MustCompile(repoLocationPattern)
)

// IsRepoPath checks the SYNTAX of a path inside a repository and nothing else. It
// does not stat, open or clone anything: the compiler runs over a document, and a
// path that is well-formed but absent is a build failure with the file in hand,
// which is where that error can actually say what is missing.
func IsRepoPath() engine.Option {
	return func(a *engine.Attribute) {
		engine.Annotate("pattern", repoPathPattern)(a)
		Validator(func(p string) error { return repoPathCheck(p, false) })(a)
	}
}

// IsRepoLocation is IsRepoPath plus `.`, the repository root. A field takes it when it
// names a directory and absent means somewhere other than the root — see repoPathRoot
// for the test and for the two fields that pass it.
func IsRepoLocation() engine.Option {
	return func(a *engine.Attribute) {
		engine.Annotate("pattern", repoLocationPattern)(a)
		Validator(func(p string) error { return repoPathCheck(p, true) })(a)
	}
}

// repoPathCheck enforces one of the two patterns and says which rule was broken.
func repoPathCheck(p string, root bool) error {
	if root && repoLocationRegex.MatchString(p) {
		return nil
	}
	if repoPathRegex.MatchString(p) {
		return nil
	}
	if p == repoPathRoot {
		return fmt.Errorf("%q is the repository root, which is not a place this field can name", p)
	}
	// THE EMPTY STRING IS NOT A PATH. `source: ""` used to pass here and compile to an
	// empty value — a fourth state beside absent, `.` and a path, meaning nothing that
	// anything states. It is what `.` was before it was given the root: a spelling
	// that survives because a checker let it through, not because it says something.
	// An attribute nobody wrote never reaches this; an author who wrote an empty value
	// meant one of the other three.
	//
	// AND THE SENTENCE SAYS ONLY WHAT THIS CHECKER KNOWS, which is the grammar: a path,
	// plus the root where the field takes it. What OMITTING the field means it does not
	// know and cannot — it is different on every field that takes a path, and each one
	// writes its own in its own Doc. This used to end "or leave it out for this
	// resource's own location", which is the bare `source`'s meaning of absent shipped
	// by a shared checker to `build/output`, to `source.git.path` and to an agent's
	// `instructions`, where it is true of none of them; and it offered `.` on the
	// fields that refuse it.
	if p == "" {
		if root {
			return fmt.Errorf("empty: write a path, or %q for the repository root", repoPathRoot)
		}
		return errors.New("empty: write a path")
	}
	// One grammar, several ways to break it. The regex has already said no;
	// these say why, in the author's terms rather than the pattern's.
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("path %q is absolute; it is relative to the repository", p)
	}
	if strings.HasSuffix(p, "/") {
		return fmt.Errorf("path %q must not end in /", p)
	}
	if strings.ContainsRune(p, '\\') {
		return fmt.Errorf("path %q uses a backslash; separate segments with /", p)
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "":
			return fmt.Errorf("path %q has an empty segment", p)
		case seg == "..":
			return fmt.Errorf("path %q climbs out of the repository with ..", p)
		case strings.Trim(seg, ".") == "":
			return fmt.Errorf("path %q has a segment that is only dots; %q alone is the "+
				"repository root, and nothing else with a dot in it means anything", p, repoPathRoot)
		}
	}
	return fmt.Errorf("path %q is not a repository path: letters, digits, "+
		"'.', '_' and '-', separated by /", p)
}

// RepoPathPattern and RepoPathChecker are the one grammar, exported so a test can pin
// them to each other. Nothing else needs them — the attribute option carries both.
func RepoPathPattern() string     { return repoPathPattern }
func RepoLocationPattern() string { return repoLocationPattern }
func RepoPathChecker() func(string) error {
	return func(p string) error { return repoPathCheck(p, false) }
}
func RepoLocationChecker() func(string) error {
	return func(p string) error { return repoPathCheck(p, true) }
}

// IsGitURL checks that a clone URL is well-formed and names a host, by DERIVING
// ITS CANONICAL KEY and reporting whether that worked. The grammar itself — both
// spellings git accepts, the credential refusal, the host, the path — lives in
// pkg/git/repokey, because the compiler is not the only thing that reads a clone
// URL: the auth store, the sources index and a webhook adapter all have to agree
// with it on which repository a URL names. It used to be a second copy of the
// parser here, and a second copy of a grammar is a grammar that drifts — the same
// failure the repo-path pattern and its checker already had.
//
// So a string this accepts is exactly a string that has a key. It resolves
// nothing: the host is not looked up, not reached, and the repository is not
// checked to exist. That is the platform's at publish, with credentials this
// compiler does not have.
func IsGitURL() engine.Option {
	return Validator(repokey.Valid)
}
