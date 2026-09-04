package tcc

// Shapes: what a resource kind is, read off its schema rather than off its name.
// A new kind in the DSL that has the same shape gets the same behaviour.

// RepoShape describes a kind whose content can come from another repository.
//
// # v2's source is one switch and the URL is the provider
//
// A resource's `source` is an Either: a bare path in this repository, another
// component of the project, a builtin, an image somebody else built, or a git
// repository. The git branch is
//
//	source: {git: <clone URL>, branch: …, path: …}
//
// and the clone URL carries the host, so GitLab, Gitea, Codeberg and an internal
// mirror are all values rather than schema edits. There is no provider key and
// no numeric id: those were GitHub facts, not git ones.
//
// THIS USED TO LOOK FOR v1's SHAPE — `source/{github}/fullname` plus an `id` —
// and found it nowhere, so Repo() returned nil for every kind and the CLI's git
// verbs were exposed on nothing at all. The vocabulary had not dropped
// per-resource repositories; it had renamed how one is written.
type RepoShape struct {
	// Kind is the source switch. Its "git" alternative is what makes a resource
	// repository-backed, and WritePath over it is how a document opens that
	// branch.
	Kind Field

	// URL, Branch and Path are where the three values live in the document.
	URL    []string
	Branch []string
	Path   []string
}

// gitBranch is the source switch's alternative that means "another repository".
const gitBranch = "git"

// Repo is the repository shape of a resource kind, nil when it has none.
func (f *Form) Repo() *RepoShape {
	s := &RepoShape{}

	for _, fd := range f.Fields {
		switch {
		case fd.IsSelector && len(fd.BranchPrefix) == 1 && fd.BranchPrefix[0] == "source":
			s.Kind = fd
		case pathIs(fd, "source", gitBranch):
			s.URL = fd.Path
		case pathIs(fd, "source", "branch"):
			s.Branch = fd.Path
		case pathIs(fd, "source", "path"):
			s.Path = fd.Path
		}
	}

	// A KIND IS REPOSITORY-BACKED WHEN ITS SWITCH OFFERS THAT BRANCH. The URL
	// alone is not enough: it has to be reachable, which means the switch has
	// to have the alternative that opens it.
	if len(s.URL) == 0 || !offers(s.Kind, gitBranch) {
		return nil
	}
	return s
}

func pathIs(fd Field, segments ...string) bool {
	if len(fd.Path) != len(segments) {
		return false
	}
	for i, seg := range segments {
		if fd.Path[i] != seg {
			return false
		}
	}
	return true
}

func offers(f Field, alternative string) bool {
	for _, a := range f.Alternatives {
		if a == alternative {
			return true
		}
	}
	return false
}

// CodeBacked reports whether a kind names where its code comes from — it
// declares a `source`.
//
// THE RULE USED TO BE "a top-level `source` that is a reference", which was v1's
// shape and matches nothing in v2: a source is a block now, and which branch of
// it an author took (a built component, a path in the repository, a git
// repository, an image) is the discriminator beside it. Under the old rule no
// kind was code-backed, so the CLI offered `run` on nothing and no test noticed,
// because the test named a kind that had been renamed.
func (f *Form) CodeBacked() bool {
	for _, fd := range f.Fields {
		if len(fd.Path) > 0 && fd.Path[0] == "source" {
			return true
		}
	}
	return false
}

// Owns reports whether a field path is part of the repository block — the
// values the repository flow chooses rather than the ones an author types.
func (s *RepoShape) Owns(path []string) bool {
	for _, owned := range [][]string{s.URL, s.Branch, s.Path} {
		if len(owned) == len(path) {
			same := true
			for i := range owned {
				if owned[i] != path[i] {
					same = false
					break
				}
			}
			if same {
				return true
			}
		}
	}
	return false
}
