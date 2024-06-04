package libraries

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (g getter) Struct() (lib *structureSpec.Library, err error) {
	provider, repoId, fullname := g.Git()
	lib = &structureSpec.Library{
		Id:          g.Id(),
		Name:        g.Name(),
		Description: g.Description(),
		Tags:        g.Tags(),
		Path:        g.Path(),
		Branch:      g.Branch(),
		Provider:    provider,
		RepoID:      repoId,
		RepoName:    fullname,
	}

	return
}
