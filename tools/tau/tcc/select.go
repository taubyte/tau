package tcc

import (
	"fmt"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

// GroupFor is the resource kind authored under a config directory.
func GroupFor(dir string) (Group, error) {
	groups, err := Groups()
	if err != nil {
		return Group{}, err
	}
	for _, g := range groups {
		if g.Dir == dir {
			return g, nil
		}
	}
	return Group{}, fmt.Errorf("no resource kind under %q", dir)
}

// SelectResource resolves one resource of a kind from --name/arg0, or offers a
// selection, and returns its name and document.
func SelectResource(ctx *cli.Context, dir string) (string, Doc, error) {
	g, err := GroupFor(dir)
	if err != nil {
		return "", nil, err
	}
	st, err := Open()
	if err != nil {
		return "", nil, err
	}
	return st.Select(ctx, g)
}

// Select resolves one resource of a kind in this store.
func (st *Store) Select(ctx *cli.Context, g Group) (string, Doc, error) {
	names, err := st.List(g.Dir)
	if err != nil {
		return "", nil, err
	}
	if len(names) == 0 {
		return "", nil, fmt.Errorf("no %s found", g.Dir)
	}

	name := ctx.String(flags.Name.Name)
	if name == "" {
		if name, err = prompts.SelectInterface(names, "Select a "+g.Name+":", ""); err != nil {
			return "", nil, err
		}
	}

	doc, err := st.Doc(g.Dir, name)
	if err != nil {
		return "", nil, fmt.Errorf("%s `%s` not found", g.Name, name)
	}
	return name, doc, nil
}

// RepositoryName is the full name of the git repository backing a resource of a
// repository-backed kind.
func RepositoryName(dir string, doc Doc) (string, error) {
	g, err := GroupFor(dir)
	if err != nil {
		return "", err
	}
	form, err := FormFor(g.Def)
	if err != nil {
		return "", err
	}
	repo := form.Repo()
	if repo == nil {
		return "", fmt.Errorf("%s is not backed by a repository", g.Name)
	}
	name, _ := Get(doc, repo.Under(doc, repo.Fullname)).(string)
	return name, nil
}
