package p2p

import (
	"fmt"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	spec "github.com/taubyte/tau/pkg/specs/common"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	"github.com/taubyte/tau/services/substrate/components/p2p/common"
	"github.com/taubyte/tau/services/substrate/components/p2p/function"
)

func (s *Service) CheckTns(matcherIface commonIface.MatchDefinition) ([]commonIface.Serviceable, error) {
	var available = make([]commonIface.Serviceable, 0)
	matcher, ok := matcherIface.(*iface.MatchDefinition)
	if !ok {
		return nil, fmt.Errorf("matcher not correct type expected (%T) got (%T)", new(common.MatchDefinition), matcherIface)
	}

	functions, commit, branch, err := s.Tns().Function().All(matcher.Project, matcher.Application, spec.DefaultBranches...).List()
	if err != nil {
		return nil, err
	}

	for _, objectPathIface := range functions {
		var serv commonIface.Serviceable
		if serv, err = function.New(s, *objectPathIface, commit, branch, matcher); err != nil {
			common.Logger.Error("Getting Serviceable function failed with:", err.Error())
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
