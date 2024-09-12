package git

/* Root returns the root directory of the repository. */
func (c *Repository) Root() string {
	return c.root
}

/* Dir returns the directory of the repository. */
func (c *Repository) Dir() string {
	if c.ephemeral {
		return c.workDir
	}

	return c.root
}
