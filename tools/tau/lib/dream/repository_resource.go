package dreamLib

func (c *CompileForRepository) Execute() error {
	args := []string{
		"inject", "compileFor",
		"--project-id", c.ProjectId,
		"--resource-id", c.ResourceId,
		"--branch", c.Branch,
		"--path", c.Path,
	}

	if len(c.ApplicationId) > 0 {
		args = append(args, []string{"--application-id", c.ApplicationId}...)
	}

	return Execute(args...)
}
