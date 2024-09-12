package dreamLib

func (i *ProdProject) Attach() error {
	return Execute("inject", "attachProdProject",
		"--project-id", i.Project.Get().Id(),
		"--git-token", i.Profile.Token,
		"--register", "false",
	)
}
