package options

func Parse(c Configurable, opts []Option) error {
	var err error
	for _, o := range opts {
		err = o(c)
		if err != nil {
			return err
		}
	}

	return nil
}
