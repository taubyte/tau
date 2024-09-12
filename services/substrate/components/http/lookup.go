package http

import (
	"fmt"

	_ "embed"

	dv "github.com/taubyte/domain-validation"
	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/services/tns"
	spec "github.com/taubyte/tau/pkg/specs/common"
	domainSpec "github.com/taubyte/tau/pkg/specs/domain"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	"github.com/taubyte/tau/pkg/specs/methods"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/components/http/function"
	"github.com/taubyte/tau/services/substrate/components/http/website"
	"github.com/taubyte/tau/services/substrate/runtime/helpers"
)

// TODO: Debug loggers

var (
	//go:embed domain_public.key
	domainValPublicKeyData []byte
	ValidResources         = []spec.PathVariable{websiteSpec.PathVariable, functionSpec.PathVariable}
)

func (s *Service) CheckTns(matcherIface commonIface.MatchDefinition) ([]commonIface.Serviceable, error) {
	matcher, ok := matcherIface.(*common.MatchDefinition)
	if !ok {
		return nil, fmt.Errorf("%#v is invalid http matcher", matcher)
	}

	host := helpers.ExtractHost(matcher.Host)
	var candidates []commonIface.Serviceable
	for _, rtype := range ValidResources {
		servKey, err := methods.HttpPath(host, rtype)
		if err != nil {
			return nil, fmt.Errorf("creating new tns path for serviceable type `%s` on host `%s` failed with: %w", rtype, host, err)
		}

		indexObject, err := s.Tns().Fetch(servKey.Versioning().Links())
		if err == nil {
			pathList, err := indexObject.Current(spec.DefaultBranches)
			if err == nil {
				candidates = append(candidates, s.handleTNSPaths(rtype, matcher, pathList)...)
			}
		}
	}

	if pick := s.getPick(matcher, candidates); pick != nil {
		var publicKey []byte
		if s.Dev() {
			publicKey = domainValPublicKeyData
		} else {
			publicKey = s.dvPublicKey
		}

		if err := domainSpec.ValidateDNS(s.config.GeneratedDomainRegExp, pick.Project(), matcher.Host, s.Dev(), dv.PublicKey(publicKey)); err != nil {
			return nil, fmt.Errorf("validating dns failed for match definition `%v` failed with: %w", *matcher, err)
		}

		return []commonIface.Serviceable{pick}, nil
	}

	return nil, fmt.Errorf("no HTTP match found for method `%s` on `https://%s%s`", matcher.Method, matcher.Host, matcher.Path)
}

func (s *Service) handleTNSPaths(stype spec.PathVariable, matcher *common.MatchDefinition, paths []tns.Path) []commonIface.Serviceable {
	candidates := make([]commonIface.Serviceable, 0, len(paths))
	for _, path := range paths {
		config, err := s.Tns().Fetch(path)
		if err == nil {
			var serv commonIface.Serviceable
			switch stype {
			case websiteSpec.PathVariable:
				serv, err = website.New(s, config, matcher)
			case functionSpec.PathVariable:
				serv, err = function.New(s, config, matcher)
			}

			if err == nil && serv != nil {
				candidates = append(candidates, serv)
			}
		}
	}

	return candidates
}

func (s *Service) getPick(matcher *common.MatchDefinition, candidates []commonIface.Serviceable) commonIface.Serviceable {
	var pick commonIface.Serviceable
	currentMatch := matcherSpec.DefaultMatch
	for _, serviceable := range candidates {
		match := serviceable.Match(matcher)
		if match == matcherSpec.HighMatch {
			pick = serviceable
			break
		}
		if match > currentMatch {
			currentMatch = match
			pick = serviceable
		}
	}

	return pick
}
