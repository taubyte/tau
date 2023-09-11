package libdream

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"

	servicesIface "github.com/taubyte/go-interfaces/services"
	commonSpecs "github.com/taubyte/go-specs/common"
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
	if slices.Contains(commonSpecs.Protocols, _service) {
		switch _service {
		case commonSpecs.TNS:
			s = u.TNS()
		default:
			ru := reflect.ValueOf(u)
			runes := []rune(_service)
			runes[0] = unicode.ToUpper(runes[0])
			serviceMethod := ru.MethodByName(string(runes))
			_s := serviceMethod.Call(nil)
			var ok bool
			if s, ok = _s[0].Interface().(servicesIface.Service); !ok {
				return ok
			}
		}
	}

	return s != nil && s.Node() != nil
}

func (s *Simple) Provides(clients ...string) error {
	notProvided := make([]string, 0)

	for _, client := range clients {
		if !slices.Contains(commonSpecs.P2PStreamProtocols, client) {
			notProvided = append(notProvided, client)
		}
	}

	if len(notProvided) > 0 {
		return fmt.Errorf("clients not provided %s", strings.Join(notProvided, ","))
	}

	return nil
}
