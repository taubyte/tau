package seer

import (
	iface "github.com/taubyte/tau/core/services/seer"
)

type Options func(*Service) error

func Resolver(resolver iface.Resolver) Options {
	return func(s *Service) error {
		s.dnsResolver = resolver
		return nil
	}
}
