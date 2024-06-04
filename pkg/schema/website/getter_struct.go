package website

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (g getter) Struct() (web *structureSpec.Website, err error) {
	provider, repoId, fullname := g.Git()
	web = &structureSpec.Website{
		Id:          g.Id(),
		Name:        g.Name(),
		Description: g.Description(),
		Tags:        g.Tags(),
		Domains:     g.Domains(),
		Paths:       g.Paths(),
		Branch:      g.Branch(),
		Provider:    provider,
		RepoID:      repoId,
		RepoName:    fullname,
		SmartOps:    g.SmartOps(),
	}

	return
}
