package dream

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"

	servicesIface "github.com/taubyte/tau/core/services"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"golang.org/x/exp/slices"
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
	if slices.Contains(commonSpecs.Services, _service) {
		switch _service {
		case commonSpecs.TNS:
			s = u.TNS()
		default:
			ru := reflect.ValueOf(u)
			runes := []rune(_service)
			runes[0] = unicode.ToUpper(runes[0])
			serviceMethod := ru.MethodByName(string(runes))
			// A registered service need not expose a typed u.<Name>() accessor.
			// Only a zero-arg, value-returning method is a usable accessor: guard
			// both before calling so a missing method, an arg-taking method
			// (Call would panic), or a no-return method (Call(nil)[0] would
			// panic) all just read as "not provided".
			if !serviceMethod.IsValid() {
				return false
			}
			mt := serviceMethod.Type()
			if mt.NumIn() != 0 || mt.NumOut() == 0 {
				return false
			}
			svc, ok := serviceMethod.Call(nil)[0].Interface().(servicesIface.Service)
			if !ok {
				return false
			}
			s = svc
		}
	}

	return s != nil && s.Node() != nil
}

func (s *Simple) Provides(clients ...string) error {
	notProvided := make([]string, 0)

	for _, client := range clients {
		if !slices.Contains(commonSpecs.P2PStreamServices, client) {
			notProvided = append(notProvided, client)
		}
	}

	if len(notProvided) > 0 {
		return fmt.Errorf("clients not provided %s", strings.Join(notProvided, ","))
	}

	return nil
}
