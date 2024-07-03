package cdk

type InitOption func() error

func Init(options ...InitOption) error {
	for _, opt := range options {
		if err := opt(); err != nil {
			return err
		}
	}

	return nil
}
