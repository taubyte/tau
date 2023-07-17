package service

type Option func(*Service) error

func Dev() Option {
	return func(s *Service) error {
		s.dev = true
		return nil
	}
}

// Note: smartops run inside the storage method, so a new method must implement it's own smartOps calls
func StorageMethod(method storageMethod) Option {
	return func(s *Service) error {
		s.storageMethod = method
		return nil
	}
}
