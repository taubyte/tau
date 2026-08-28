package schema

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

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
		String("source", IsRepoPath(), InSection(section),
			Doc("Source", "Where in this repository the content is, e.g. \"agents/support\". Omitted means the resource's own location.")),
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

// repoPathPattern is the grammar, and repoPathChecker enforces exactly it. They were
// written to "mirror" each other and did not: the pattern accepted `a/../b` while the
// checker refused it, and the checker accepted `café/x` and a whole English sentence
// while the pattern refused them. An editor validating against one and a compiler
// enforcing the other disagree about the same document, which is the failure the
// introspectable pattern exists to prevent — so there is now one grammar, and the
// checker's only job is to say WHICH rule was broken.
const repoPathPattern = `^` + repoPathSegment + `(/` + repoPathSegment + `)*$`

var repoPathRegex = regexp.MustCompile(repoPathPattern)

// IsRepoPath checks the SYNTAX of a path inside a repository and nothing else. It
// does not stat, open or clone anything: the compiler runs over a document, and a
// path that is well-formed but absent is a build failure with the file in hand,
// which is where that error can actually say what is missing.
func IsRepoPath() engine.Option {
	return func(a *engine.Attribute) {
		engine.Annotate("pattern", repoPathPattern)(a)
		Validator(repoPathCheck)(a)
	}
}

// repoPathCheck enforces repoPathPattern and says which rule was broken.
func repoPathCheck(p string) error {
	if p == "" || repoPathRegex.MatchString(p) {
		return nil
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
			return fmt.Errorf("path %q has a segment that is only dots; write the path to the file", p)
		}
	}
	return fmt.Errorf("path %q is not a repository path: letters, digits, "+
		"'.', '_' and '-', separated by /", p)
}

// RepoPathPattern and RepoPathChecker are the one grammar, exported so a test can pin
// them to each other. Nothing else needs them — the attribute option carries both.
func RepoPathPattern() string             { return repoPathPattern }
func RepoPathChecker() func(string) error { return repoPathCheck }

// scpLike matches git's other spelling, git@host:owner/repo — a URL to git and not
// to net/url, which parses it as a path with no host. The trailing group is the
// repository, so it has to be more than "/": the https branch refuses a URL naming a
// host and nothing else, and this form was not held to the same rule.
var scpLike = regexp.MustCompile(`^([^\s/@]+)@([A-Za-z0-9.-]+):(.*)$`)

// IsGitURL checks that a clone URL is well-formed and names a host. It does not
// resolve the host, reach it, or care whether the repository exists — that is the
// platform's at publish, with credentials this compiler does not have.
func IsGitURL() engine.Option {
	return Validator(func(s string) error {
		if s == "" {
			return fmt.Errorf("names no repository; write the clone URL, e.g. https://gitlab.com/acme/repo")
		}
		if m := scpLike.FindStringSubmatch(s); m != nil {
			// `git@host:owner/repo`. The part before `@` is the SSH ACCOUNT, which is
			// identity and not a secret — until it carries a `:`, at which point it is
			// a user and a password and the same objection applies as to the URL form.
			if strings.Contains(m[1], ":") {
				return fmt.Errorf("%q carries a credential; a repository URL in config names the repository, and the identity is the platform's to hold", s)
			}
			// The same rule the URL branch enforces, which this form was not held to.
			if strings.Trim(m[3], "/") == "" {
				return fmt.Errorf("%q names a host but no repository", s)
			}
			return nil
		}
		u, err := url.Parse(s)
		if err != nil {
			return fmt.Errorf("not a URL: %w", err)
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git":
		case "":
			return fmt.Errorf("%q has no scheme; write the clone URL, e.g. https://gitlab.com/acme/repo", s)
		default:
			return fmt.Errorf("scheme %q is not a git transport", u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("%q names no host", s)
		}
		// A TOKEN IN TRACKED CONFIG. `https://user:ghp_xxx@github.com/acme/repo` is a
		// working clone URL and a secret in git, and it is the one shape of this
		// field that cannot be fixed by rotating anything else. §10a: config is
		// portable and templatable, so the identity a clone needs is held beside the
		// deployment and never written into the document that describes it.
		if u.User != nil {
			return fmt.Errorf("%q carries a credential; a repository URL in config names the repository, and the identity is the platform's to hold", s)
		}
		if strings.Trim(u.Path, "/") == "" {
			return fmt.Errorf("%q names a host but no repository", s)
		}
		return nil
	})
}
