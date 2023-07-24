package database

type Option func(*Service) error

func Dev() Option {
	return func(d *Service) error {
		d.dev = true
		return nil
	}
}
