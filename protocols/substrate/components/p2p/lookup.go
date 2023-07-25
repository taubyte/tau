package p2p

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	matcherSpec "github.com/taubyte/go-specs/matcher"
	"github.com/taubyte/odo/protocols/substrate/components/p2p/common"
	"github.com/taubyte/odo/protocols/substrate/components/p2p/function"
)

func (s *Service) CheckTns(matcherIface commonIface.MatchDefinition) ([]commonIface.Serviceable, error) {
	var available = make([]commonIface.Serviceable, 0)
	matcher, ok := matcherIface.(*iface.MatchDefinition)
	if !ok {
		return nil, fmt.Errorf("matcher not correct type expected (%T) got (%T)", new(common.MatchDefinition), matcherIface)
	}

	functions, err := s.Tns().Function().All(matcher.Project, matcher.Application, s.Branch()).List()
	if err != nil {
		return nil, err
	}

	for _, objectPathIface := range functions {
		var serv commonIface.Serviceable
		if serv, err = function.New(s, *objectPathIface, matcher); err != nil {
			common.Logger.Errorf("Getting Serviceable function failed with: %s", err)
			continue
		}

		available = append(available, serv)
	}

	for _, serviceable := range available {
		if serviceable.Match(matcher) == matcherSpec.HighMatch {
			return []commonIface.Serviceable{serviceable}, nil
		}
	}

	return nil, fmt.Errorf("no P2P match found from given matcher `%v`", matcher)
}
