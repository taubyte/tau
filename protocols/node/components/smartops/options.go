package smartOps

type Option func(*Service) error

func Dev() Option {
	return func(s *Service) error {
		s.dev = true
		return nil
	}
}

func Verbose() Option {
	return func(s *Service) error {
		s.verbose = true
		return nil
	}
}
