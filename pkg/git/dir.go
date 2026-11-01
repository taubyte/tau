package git

// Dir returns the directory of the repository.
func (c *Repository) Dir() string {
	return c.root
}
