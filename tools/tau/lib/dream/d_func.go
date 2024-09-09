package dreamLib

func (c *CompileForDFunc) Execute() error {
	args := []string{
		"inject", "compileFor",
		"--project-id", c.ProjectId,
		"--resource-id", c.ResourceId,
		"--branch", c.Branch,
		"--call", c.Call,
		"--path", c.Path,
	}

	if len(c.ApplicationId) > 0 {
		args = append(args, []string{"--application-id", c.ApplicationId}...)
	}

	return Execute(args...)
}
