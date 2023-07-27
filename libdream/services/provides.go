package services

import (
	"fmt"
	"strings"

	servicesIface "github.com/taubyte/go-interfaces/services"
)

func (u *Universe) Provides(services ...string) error {
	notProvided := make([]string, 0)

	for _, service := range services {
		if !u.provided(service) {
			notProvided = append(notProvided, service)
		}
	}

	if len(notProvided) > 0 {
		return fmt.Errorf("services not provided %s", strings.Join(notProvided, ","))
	}

	return nil
}

func (u *Universe) provided(_service string) bool {
	var s servicesIface.Service

	switch _service {
	case "auth":
		s = u.Auth()
	case "hoarder":
		s = u.Hoarder()
	case "monkey":
		s = u.Monkey()
	case "patrick":
		s = u.Patrick()
	case "seer":
		s = u.Seer()
	case "tns":
		s = u.TNS()
	case "substrate":
		s = u.Substrate()
	default:
		return false
	}

	if s == nil || s.Node() == nil {
		return false
	}

	return true
}

func (s *Simple) Provides(clients ...string) error {
	notProvided := make([]string, 0)

	for _, client := range clients {
		if !s.provided(client) {
			notProvided = append(notProvided, client)
		}
	}

	if len(notProvided) > 0 {
		return fmt.Errorf("clients not provided %s", strings.Join(notProvided, ","))
	}

	return nil
}

func (s *Simple) provided(client string) bool {
	switch client {
	case "auth", "hoarder", "monkey", "patrick", "seer", "tns":
		return true
	default:
		return false
	}
}
