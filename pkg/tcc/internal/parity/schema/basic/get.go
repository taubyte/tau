package basic

func Get[T any](c ConfigIface, path ...string) (value T) {
	config := c.Config()
	for _, p := range path {
		config.Get(p)
	}

	config.Value(&value)
	return
}
