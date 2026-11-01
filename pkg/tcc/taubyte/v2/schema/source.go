package schema

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// contentSource is where a resource's content comes from: one optional switch.
//
//	(nothing)                     this repository, at the resource's own location
//	source: agents/support        this repository, at that path
//	source: {component: paylib}   another component of this project (+ `namespace`)
//	source: {builtin: agent}      a program the platform ships; a lowering sets it
//	source: {git: <url>, branch: …, path: …}   another repository, by its clone URL
//	source: {image: <ref>}        an artifact somebody else already built
//
// An image is a source branch, so image and another source cannot both be
// written. The clone URL carries the forge, so any git host is a value.
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
				// The pull credential is a named secret, inside the only branch it means anything
				// in.
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
				// PerEnv, so a repository's content follows the environment like the config
				// does; omitted is the repository's default branch.
				//
				//	branch:         main
				//	branch_staging: develop
				String(Key("source", "branch"), PerEnv(), InSection(section), ShowWhen(Key("source", "kind"), "git"),
					Doc("Branch", "Branch to read, e.g. \"main\". Per environment: `branch_staging: develop`. Omitted means the repository's default branch.")),
				String(Key("source", "path"), IsRepoPath(), InSection(section), ShowWhen(Key("source", "kind"), "git"),
					Doc("Path", "Where in that repository the content is. Omitted means its root.")),

				// Lets serving config live in the site's own repository. Only the blocks this
				// vocabulary delegates may come from there; a file minting a resource, writing
				// identity or opening access is refused, and a field set in both places is refused
				// naming both.
				String(Key("source", "config"), IsRepoPath(), InSection(section), ShowWhen(Key("source", "kind"), "git"),
					Doc("Config", "A file in that repository carrying this resource's serving block, e.g. \"taubyte.yaml\". Read at the commit its artifact was built from. What it may declare is fixed: the blocks this kind delegates, and nothing that names or reaches anything.")))
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

// repoPathRoot is the repository root. A field may take it only when absent means
// somewhere else (the bare `source`, `build/output`); where absent already means
// the root, `.` would be a second spelling.
const repoPathRoot = "."

// repoPathPattern is the one grammar; repoPathChecker enforces exactly it and only
// says which rule was broken, so an editor and the compiler agree.
const repoPathPattern = `^` + repoPathSegment + `(/` + repoPathSegment + `)*$`

// repoLocationPattern adds the root, for a field naming a directory whose absent
// value means elsewhere. A field naming a file never takes it.
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
	// The empty string is not a path. The message states only the grammar: what
	// omitting the field means differs per field and lives in its Doc.
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

// IsGitURL accepts a clone URL exactly when pkg/git/repokey can derive its key,
// so every reader agrees on which repository a URL names. It resolves nothing.
func IsGitURL() engine.Option {
	return Validator(repokey.Valid)
}
