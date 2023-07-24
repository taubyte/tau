package nodehttp

type Option func(*Service) error

func DvKey(publicKey []byte) Option {
	return func(s *Service) error {
		s.dvPublicKey = publicKey
		return nil
	}
}
