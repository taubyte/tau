package website

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	repositoryCommands "github.com/taubyte/tau/tools/tau/cli/commands/resources/repository"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
)

type wrapped struct {
	resource *structureSpec.Website
}

type setter struct {
	resource *structureSpec.Website
}

type getter struct {
	resource *structureSpec.Website
}

func (rw wrapped) Set() repositoryCommands.Setter {
	return &setter{rw.resource}
}

func (rw wrapped) Get() repositoryCommands.Getter {
	return &getter{rw.resource}
}

func (rw wrapped) UnWrap() *structureSpec.Website {
	return rw.resource
}

func (g getter) Name() string {
	return g.resource.Name
}

func (g getter) Description() string {
	return g.resource.Description
}

func (g getter) RepoName() string {
	return g.resource.RepoName
}

func (g getter) RepoID() string {
	return g.resource.RepoID
}

func (g getter) Branch() string {
	return g.resource.Branch
}

func (g getter) RepositoryURL() string {
	return repositoryLib.GetRepositoryUrl(g.resource.Provider, g.resource.RepoName)
}

func (s setter) RepoID(id string) {
	s.resource.RepoID = id
}

func (s setter) RepoName(name string) {
	s.resource.RepoName = name
}

func Wrap(resource *structureSpec.Website) repositoryCommands.Resource {
	return wrapped{resource}
}
