package tcc

// RepositoryNames lists the git repository each repository-backed resource in
// the project points at, across the project scope and every container
// (application) scope. Which kinds are repository-backed, and where their
// repository name sits, both come from the DSL — nothing here knows that
// websites and libraries are the ones with repos.
func (st *Store) RepositoryNames() ([]string, error) {
	groups, err := Groups()
	if err != nil {
		return nil, err
	}

	scopes := [][]string{nil}
	for _, g := range groups {
		if !g.Container {
			continue
		}
		names, err := st.s.List([]string{g.Dir})
		if err != nil {
			continue // no instances of that container
		}
		for _, name := range names {
			scopes = append(scopes, []string{g.Dir, name})
		}
	}

	var out []string
	for _, g := range groups {
		form, err := FormFor(g.Def)
		if err != nil {
			return nil, err
		}
		repo := form.Repo()
		if repo == nil {
			continue
		}
		for _, scope := range scopes {
			dir := append(append([]string{}, scope...), g.Dir)
			names, err := st.s.List(dir)
			if err != nil {
				continue
			}
			for _, name := range names {
				doc, err := st.s.Read(append(append([]string{}, dir...), name))
				if err != nil {
					continue
				}
				if full, ok := Get(Doc(doc), repo.Under(doc, repo.Fullname)).(string); ok && full != "" {
					out = append(out, full)
				}
			}
		}
	}
	return out, nil
}
