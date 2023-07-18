package cli

func Start(args ...string) error {
	app, err := Build()
	if err != nil {
		return err
	}

	err = app.Run(args)
	if err != nil {
		return err
	}

	return nil
}
