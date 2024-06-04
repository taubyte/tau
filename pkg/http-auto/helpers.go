package auto

import (
	"errors"
	"fmt"
	"strings"

	"github.com/taubyte/tau/core/services/tns"
	domainSpecs "github.com/taubyte/tau/pkg/specs/domain"
	"github.com/taubyte/tau/pkg/specs/extract"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/utils/maps"
)

func (s *Service) validateFromTns(hostname string) (string, error) {
	err := s.checkDomainRegistration(hostname)
	if err != nil {
		return "", fmt.Errorf("failed checkDomainRegistration of %s with %v", hostname, err)
	}

	return s.getProjectId(hostname)
}

func (s *Service) checkDomainRegistration(hostname string) error {
	// Make sure the domain is registered inside tns
	tnsPath, err := domainSpecs.Tns().BasicPath(hostname)
	if err != nil {
		return fmt.Errorf("failed creating basic tns path for %s with %v", hostname, err)
	}

	tnsInterface, err := s.tnsClient.Lookup(tns.Query{
		Prefix: tnsPath.Slice(),
		RegEx:  false,
	})
	if err != nil {
		return fmt.Errorf("failed tns lookup on %s with %v", hostname, err)
	}

	domPath, ok := tnsInterface.([]string)
	if !ok {
		return errors.New("failed converting tns response to string")
	}

	if len(domPath) == 0 {
		return fmt.Errorf("domain key from tns is empty")
	}

	return nil
}

func (s *Service) getProjectId(name string) (string, error) {
	// Grab projectID using specs
	var tnsPath tns.Object
	p, err := functionSpec.Tns().HttpPath(name)
	if err != nil {
		return "", fmt.Errorf("failed functionSpec HttpPath with %v", err)
	}

	// Check function to grab projcetID. If fails check website. If fails then not found
	tnsPath, err = s.tnsClient.Fetch(p)
	if err != nil || tnsPath.Interface() == nil {
		p, err = websiteSpec.Tns().HttpPath(name)
		if err != nil {
			return "", fmt.Errorf("failed websiteSpec HttpPath with %v", err)
		}

		tnsPath, err = s.tnsClient.Fetch(p)
		if err != nil {
			return "", fmt.Errorf("failed tns fetch with %v", err)
		}

		if tnsPath.Interface() == nil {
			return "", errors.New("fetch returned a nil interface")
		}
	}

	//Interface might have multiple keys
	ret, err := getCorrectInterface(tnsPath)
	if err != nil {
		return "", err
	}

	var projectId string
	for _, _interface := range ret {
		ret2, ok := _interface.(string)
		if !ok {
			return "", errors.New("failed converting interface to string")
		}

		parser, err := extract.Tns().BasicPath(ret2)
		if err != nil {
			return "", fmt.Errorf("failed extract tns basic path with %v", err)

		}

		projectId = parser.Project()
	}

	return strings.ToLower(projectId), nil
}

func getCorrectInterface(tnsObj tns.Object) ([]interface{}, error) {
	var ret []interface{}
	var ok bool
	// Try to find "links"
	mapInterface, err := maps.InterfaceToStringKeys(tnsObj.Interface())
	if err == nil {
		interfacePath, ok := mapInterface["links"]
		if !ok {
			return nil, errors.New("did not find `links` inside map")
		}

		ret, ok = interfacePath.([]interface{})
		if !ok {
			return nil, errors.New("failed converting interfacePath tp []interface{}")
		}
	} else {
		// Else it has a key
		ret, ok = tnsObj.Interface().([]interface{})
		if !ok {
			return nil, errors.New("failed converting tnsObj to []interface{}")
		}
	}

	return ret, nil
}
