package project

import (
	"github.com/taubyte/tau/pkg/schema/basic"
)

func Open(options ...Option) (Project, error) {
	project := &project{}

	for _, opt := range options {
		err := opt(project)
		if err != nil {
			return nil, err
		}
	}

	var err error
	project.Resource, err = basic.NewNoName(project.seer, project)
	if err != nil {
		// Unreachable
		return nil, err
	}

	project.Resource.Root = project.Root
	project.Resource.Config = project.Config

	return project, nil
}
