package nodehttp

import (
	"fmt"

	_ "embed"

	"bitbucket.org/taubyte/go-node-tvm/helpers"
	dv "github.com/taubyte/domain-validation"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/common"
	"github.com/taubyte/go-interfaces/services/tns"
	spec "github.com/taubyte/go-specs/common"
	domainSpec "github.com/taubyte/go-specs/domain"
	functionSpec "github.com/taubyte/go-specs/function"
	matcherSpec "github.com/taubyte/go-specs/matcher"
	"github.com/taubyte/go-specs/methods"
	websiteSpec "github.com/taubyte/go-specs/website"
	"github.com/taubyte/odo/protocols/node/components/http/common"
	"github.com/taubyte/odo/protocols/node/components/http/function"
	"github.com/taubyte/odo/protocols/node/components/http/website"
)

var (
	//go:embed domain_public.key
	domainValPublicKeyData []byte
	TheServiceables        = []spec.PathVariable{websiteSpec.PathVariable, functionSpec.PathVariable}
)

func (s *Service) CheckTns(matcherIface commonIface.MatchDefinition) ([]commonIface.Serviceable, error) {
	matcher, ok := matcherIface.(*common.MatchDefinition)
	if !ok {
		return nil, fmt.Errorf("%#v is invalid http matcher", matcher)
	}

	_host := helpers.ExtractHost(matcher.Host)
	var candidates []commonIface.Serviceable
	for _, stype := range TheServiceables {
		servKey, err := methods.HttpPath(_host, stype)
		if err != nil {
			return nil, fmt.Errorf("creating new tns path for serviceable type `%s` on host `%s` failed with: %s", stype, _host, err)
		}

		indexObject, err := s.Tns().Fetch(servKey.Versioning().Links())
		if err == nil {
			pathList, err := indexObject.Current(s.Branch())
			if err == nil {
				candidates = append(candidates, s.handleTNSPaths(stype, matcher, pathList)...)
			}
		}
	}

	if pick := s.getPick(matcher, candidates); pick != nil {
		project, err := pick.Project()
		if err != nil {
			return nil, fmt.Errorf("checking serviceable pick's project ID failed with: %s", err)
		}

		var publicKey []byte
		if s.dev {
			publicKey = domainValPublicKeyData
		} else {
			publicKey = s.dvPublicKey
		}

		if err = domainSpec.ValidateDNS(project.String(), matcher.Host, s.dev, dv.PublicKey(publicKey)); err != nil {
			return nil, fmt.Errorf("validating dns failed for match definition `%v` failed with: %s", *matcher, err)
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
