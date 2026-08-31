package builders

/******************** Backwards Compatibility  ************************/

func (c *Config) HandleDepreciatedEnvironment() (environment Environment) {
	if len(c.Environment.Image) == 0 {
		return c.Enviroment
	}

	return c.Environment
}
