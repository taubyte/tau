package tcc

// RepositoryNames lists the git repository each repository-backed resource in
// the project points at, across the project scope and every container scope.
// Which kinds are repository-backed, and where the URL sits, both come from the
// DSL — nothing here knows which kinds have one.
//
// WHAT IT RETURNS IS A CLONE URL, because that is what v2 writes: the host is
// the provider and there is nothing else naming it.
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
				v, err := st.s.Get(append(append([]string{}, dir...), name), nil)
				if err != nil {
					continue
				}
				doc, _ := v.(map[string]any)
				if url, ok := Get(Doc(doc), repo.URL).(string); ok && url != "" {
					out = append(out, url)
				}
			}
		}
	}
	return out, nil
}
