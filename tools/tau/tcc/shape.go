package tcc

// Shapes: what a resource kind is, read off its schema rather than off its name.
// A new kind in the DSL that has the same shape gets the same behaviour.

// RepoShape describes a kind backed by a git repository: the DSL gives it a
// source/<provider>/fullname block, with the provider a dynamic key.
type RepoShape struct {
	Provider Field // dynamic selector over source/{github|...}
	Branch   []string
	Fullname string // leaf key under source/<provider>
	ID       string
}

// Repo is the repository shape of a resource kind, nil when it has none.
func (f *Form) Repo() *RepoShape {
	s := &RepoShape{}
	for _, fd := range f.Fields {
		switch {
		case fd.IsSelector && len(fd.BranchPrefix) == 1 && fd.BranchPrefix[0] == "source":
			s.Provider = fd
		case len(fd.Path) == 2 && fd.Path[0] == "source" && fd.Path[1] == "branch":
			s.Branch = fd.Path
		case len(fd.Path) == 3 && fd.Path[0] == "source" && fd.Path[2] == "fullname":
			s.Fullname = fd.Path[2]
		case len(fd.Path) == 3 && fd.Path[0] == "source" && fd.Path[2] == "id":
			s.ID = fd.Path[2]
		}
	}
	if s.Fullname == "" || s.ID == "" || len(s.Provider.Alternatives) == 0 {
		return nil
	}
	return s
}

// Under is the path of a leaf of the active provider's block.
func (s *RepoShape) Under(doc Doc, leaf string) []string {
	return append(WritePath(doc, s.Provider), leaf)
}

// CodeBacked reports whether a kind names where its code comes from — it
// declares a `source`.
//
// THE RULE USED TO BE "a top-level `source` that is a reference", which was v1's
// shape and matches nothing in v2: a source is a block now, and which branch of
// it an author took (a built component, a path in the repository, a builtin) is
// the discriminator beside it. Under the old rule no kind was code-backed, so
// the CLI offered `run` on nothing and no test noticed, because the test named a
// kind that had been renamed.
//
// The old doc, kept because it says what this used to mean: the
// DSL gives it a scalar `source` that either points at a library or is "." for
// inline code.
func (f *Form) CodeBacked() bool {
	for _, fd := range f.Fields {
		if len(fd.Path) > 0 && fd.Path[0] == "source" {
			return true
		}
	}
	return false
}
