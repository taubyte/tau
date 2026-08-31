// Package repokey derives one canonical identity from any spelling of a git
// clone URL.
//
// V2'S CONFIG NAMES A REPOSITORY BY CLONE URL. There is no provider field and no
// numeric repository id anywhere in the vocabulary — the host in the URL is the
// provider, which is what makes GitLab, Gitea, Codeberg and an internal mirror
// values rather than schema edits. The cost of that is that the same repository
// has many legal spellings, and everything that indexes one — the auth store, the
// sources index, a webhook adapter deciding which project a push belongs to — has
// to agree on which of them it is filed under. Two of those computing "the same"
// name privately is two grammars, and they drift.
//
// So there is one grammar, here, and it produces one key:
//
//	https://github.com/acme/pay          -> github.com/acme/pay
//	https://github.com/Acme/Pay.git      -> github.com/acme/pay
//	https://github.com:443/acme/pay/     -> github.com/acme/pay
//	ssh://git@github.com/acme/pay        -> github.com/acme/pay
//	ssh://git@github.com:22/acme/pay     -> github.com/acme/pay
//	git@github.com:acme/pay              -> github.com/acme/pay
//	git@github.com:/acme/pay.git         -> github.com/acme/pay
//
// THE SCP FORM AND THE ssh:// FORM ARE THE SAME REPOSITORY, and that is the pair
// that had to be decided. `git@host:owner/repo` is git's own second spelling of
// `ssh://git@host/owner/repo`; the leading `/` the URL form needs and the scp form
// does not is punctuation, and the `git@` is the SSH ACCOUNT — an identity, not
// part of the repository's name — so both forms drop it. They agree because the
// key keeps only the host and the path.
//
// They disagree in exactly one place, and git decides it, not this package: THE
// SCP FORM HAS NO PORT. Git only recognises that form when there is no `/` before
// the first `:`, and everything after the `:` is the path, so `git@host:22/a/b` is
// the repository `22/a/b` and not `a/b` on port 22. Reading it as a port here
// would file a repository under a key naming a repository a clone would never
// fetch. Write `ssh://git@host:22/a/b` when a port is meant.
//
// The package imports nothing outside the standard library — deliberately, the
// same reason core/common/repositorytype exists. Every consumer of the key is on
// one side or the other of the wasm boundary, and a leaf builds under GOOS=js.
package repokey

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Key is a repository's canonical identity: the host that serves it and the path
// it is served at, with everything that varies between spellings removed.
//
// Owner is EVERYTHING before the last segment, not the first segment, because a
// GitLab subgroup nests: `gitlab.com/acme/team/widget` has owner `acme/team`.
// Namespace is the first segment, which is the unit a tenancy owns.
//
// Owner is empty for a bare repository served at a host's root, which self-hosted
// git does and no forge does.
type Key struct {
	// Host is lowercased. A port is present only when it is not the transport's
	// default, and an IPv6 literal keeps its brackets, so Host is always something
	// that can be spliced back into a URL.
	Host string
	// Owner is the lowercased path above the repository, "/"-joined, or empty.
	Owner string
	// Name is the lowercased last path segment with any ".git" removed.
	Name string
}

// String is the key: host, then the path under it. This is the form that goes in
// an index, a job id or a kvdb key path — one line, no punctuation that needs
// escaping beyond the ":" a non-default port brings.
func (k Key) String() string {
	if k.Owner == "" {
		return k.Host + "/" + k.Name
	}
	return k.Host + "/" + k.Owner + "/" + k.Name
}

// FullName is what a provider calls the repository — `owner/name`, the shape the
// GitHub API returns and the shape a tenancy check has always been written
// against. It drops the host, so it is NOT an identity across providers; use
// String for that.
func (k Key) FullName() string {
	if k.Owner == "" {
		return k.Name
	}
	return k.Owner + "/" + k.Name
}

// Namespace is the owning segment — the org, group or workspace. It is the first
// segment of Owner rather than all of it: a subgroup belongs to its group, and it
// is the group that a cloud is scoped to.
func (k Key) Namespace() string {
	ns, _, found := strings.Cut(k.Owner, "/")
	if !found {
		return k.Owner
	}
	return ns
}

// scpLike matches git's other spelling, git@host:owner/repo — a URL to git and
// not to net/url, which parses it as a path with no host. The account before the
// "@" may not contain "/" or whitespace, and the path may not contain whitespace,
// which is what makes this form and the URL form refuse the same strings.
var scpLike = regexp.MustCompile(`^([^\s/@]+)@([A-Za-z0-9.-]+):(\S*)$`)

// defaultPort is the port each transport is spoken on when the URL does not say
// so. A URL that spells the default out names the same repository as one that
// leaves it out, so the key drops it — and keeps any other port, because that is
// a different server.
var defaultPort = map[string]string{
	"https": "443",
	"http":  "80",
	"ssh":   "22",
	"git":   "9418",
}

// Parse derives the canonical key from a clone URL, or says why the string is not
// one. It resolves nothing: the host is not looked up, not reached, and the
// repository is not checked to exist. That is the platform's job at publish, with
// credentials this has no business holding.
func Parse(cloneURL string) (Key, error) {
	if cloneURL == "" {
		return Key{}, fmt.Errorf("names no repository; write the clone URL, e.g. https://gitlab.com/acme/repo")
	}

	host, path, err := hostAndPath(cloneURL)
	if err != nil {
		return Key{}, err
	}

	// Leading and trailing slashes are punctuation: they are the only thing
	// separating `git@host:acme/pay` from `ssh://git@host/acme/pay`, and a
	// trailing one is what a browser's address bar adds.
	segments := strings.Split(strings.ToLower(strings.Trim(path, "/")), "/")
	if len(segments) == 1 && segments[0] == "" {
		return Key{}, fmt.Errorf("%q names a host but no repository", cloneURL)
	}
	for _, s := range segments {
		switch {
		case s == "":
			return Key{}, fmt.Errorf("%q has an empty path segment", cloneURL)
		case s == "." || s == "..":
			return Key{}, fmt.Errorf("%q has a %q path segment; a clone URL names a repository, it does not climb to one", cloneURL, s)
		case strings.ContainsAny(s, " \t\r\n"):
			return Key{}, fmt.Errorf("%q has whitespace in its path", cloneURL)
		}
	}

	// ".git" is the suffix a server answers to and not part of the name, which is
	// why one repository is cloned by two URLs that differ only in it. Stripping
	// happens after the lowercase, so ".GIT" goes with it.
	last := len(segments) - 1
	segments[last] = strings.TrimSuffix(segments[last], ".git")
	if segments[last] == "" {
		return Key{}, fmt.Errorf("%q names a host but no repository", cloneURL)
	}

	return Key{
		Host:  host,
		Owner: strings.Join(segments[:last], "/"),
		Name:  segments[last],
	}, nil
}

// String derives the key and returns it in one step, for a caller that wants the
// index entry and not the parts.
func String(cloneURL string) (string, error) {
	k, err := Parse(cloneURL)
	if err != nil {
		return "", err
	}
	return k.String(), nil
}

// Valid reports the error a clone URL has, or nil. It is Parse with the key
// dropped, and it exists so a validator cannot be a SECOND reading of the same
// grammar — the check and the key are one function, and a string that validates
// is exactly a string that has a key.
func Valid(cloneURL string) error {
	_, err := Parse(cloneURL)
	return err
}

// hostAndPath splits a clone URL in the two spellings git accepts, and refuses a
// credential in either.
func hostAndPath(cloneURL string) (host, path string, err error) {
	if m := scpLike.FindStringSubmatch(cloneURL); m != nil {
		// `git@host:owner/repo`. The part before "@" is the SSH ACCOUNT, which is
		// identity and not a secret — until it carries a ":", at which point it is
		// a user and a password and the objection below applies to it too.
		if strings.Contains(m[1], ":") {
			return "", "", credentialError(cloneURL)
		}
		return strings.ToLower(m[2]), m[3], nil
	}

	u, parseErr := url.Parse(cloneURL)
	if parseErr != nil {
		return "", "", fmt.Errorf("not a URL: %w", parseErr)
	}
	switch u.Scheme {
	case "https", "http", "ssh", "git":
	case "":
		return "", "", fmt.Errorf("%q has no scheme; write the clone URL, e.g. https://gitlab.com/acme/repo", cloneURL)
	default:
		return "", "", fmt.Errorf("scheme %q is not a git transport", u.Scheme)
	}

	name := u.Hostname()
	if name == "" {
		return "", "", fmt.Errorf("%q names no host", cloneURL)
	}

	// A TOKEN IN A CLONE URL. `https://user:ghp_xxx@github.com/acme/repo` is a
	// working clone URL and, once written down, a secret at rest — and it is the
	// one shape of this field that rotating something else cannot fix. The
	// identity a clone needs is held beside the deployment, never in the string
	// that names the repository.
	//
	// THE PASSWORD, NOT THE USER. `ssh://git@github.com/acme/repo` carries a
	// username because that is how the transport is spelled, and it is the same
	// `git` the scp form puts before the "@" for the same repository. Refusing the
	// whole userinfo refused every ssh URL while accepting the scp form beside it,
	// which is one rule applied to one spelling of one thing.
	if pw, has := u.User.Password(); has && pw != "" {
		return "", "", credentialError(cloneURL)
	}

	host = strings.ToLower(name)
	if strings.Contains(host, ":") {
		// An IPv6 literal, which net/url hands back unbracketed.
		host = "[" + host + "]"
	}
	if p := u.Port(); p != "" && p != defaultPort[u.Scheme] {
		host += ":" + p
	}
	return host, u.Path, nil
}

func credentialError(cloneURL string) error {
	return fmt.Errorf("%q carries a credential; a clone URL names the repository, and the identity is the platform's to hold", cloneURL)
}
