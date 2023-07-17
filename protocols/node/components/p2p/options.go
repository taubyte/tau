package p2p

type Option func(*Service) error

func Verbose() Option {
	return func(s *Service) error {
		s.verbose = true
		return nil
	}
}

func Dev() Option {
	return func(s *Service) error {
		s.dev = true
		return nil
	}
}
