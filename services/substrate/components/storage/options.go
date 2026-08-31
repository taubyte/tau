package substrate

type Option func(*Service) error

func StorageMethod(method storageMethod) Option {
	return func(s *Service) error {
		s.storageMethod = method
		return nil
	}
}
