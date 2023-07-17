package service

import (
	iface "github.com/taubyte/go-interfaces/services/seer"
)

type Options func(*Service) error

func Resolver(resolver iface.Resolver) Options {
	return func(s *Service) error {

		// TODO: Aron
		// if resolver == nil {
		// 	return errors.New("Resolver cannot be nil")
		// }

		s.dnsResolver = resolver

		return nil
	}
}
