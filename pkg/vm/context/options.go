package context

type Option func(*vmContext) error

func Project(projectId string) Option {
	return func(ctx *vmContext) error {
		ctx.projectId = projectId
		return nil
	}
}

func Application(applicationId string) Option {
	return func(ctx *vmContext) error {
		ctx.applicationId = applicationId
		return nil
	}
}

func Resource(resourceId string) Option {
	return func(ctx *vmContext) error {
		ctx.resourceId = resourceId
		return nil
	}
}

func Branch(branch string) Option {
	return func(ctx *vmContext) error {
		ctx.branches = append(ctx.branches, branch)
		return nil
	}
}

func Branches(branches ...string) Option {
	return func(ctx *vmContext) error {
		ctx.branches = branches
		return nil
	}
}

func Commit(commit string) Option {
	return func(ctx *vmContext) error {
		ctx.commit = commit
		return nil
	}
}
