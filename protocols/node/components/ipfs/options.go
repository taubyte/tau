package ipfs

func Public() Option {
	return func(s *Service) error {
		s.private = false
		return nil
	}
}

func Listen(listen []string) Option {
	return func(s *Service) error {
		s.swarmListen = listen
		return nil
	}
}

func Announce(announce []string) Option {
	return func(s *Service) error {
		s.swarmListen = announce
		return nil
	}
}

func PrivateKey(key []byte) Option {
	return func(s *Service) error {
		s.privateKey = key
		return nil
	}
}
