package p2p

import (
	"context"
	"errors"
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/substrate/p2p"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/node/components/p2p/stream"
)

func (srv *Service) LookupService(matcher *iface.MatchDefinition) (*structureSpec.Service, string, error) {
	serviceMap, err := srv.Tns().Service().Global(matcher.Project, srv.Branch()).List()
	if err != nil {
		return nil, "", fmt.Errorf("fetching services for protocol `%s` failed with: %v", matcher.Protocol, err)
	}

	var serviceApplication string
	var foundService *structureSpec.Service
	for _, service := range serviceMap {
		if service.Protocol == matcher.Protocol {
			foundService = service
			break
		}
	}

	if len(matcher.Application) > 0 {
		if foundService == nil || len(foundService.Id) == 0 {
			serviceMap, err = srv.Tns().Service().Relative(matcher.Project, matcher.Application, srv.Branch()).List()
			if err != nil {
				return nil, "", fmt.Errorf("fetching services for protocol `%s` failed with: %v", matcher.Protocol, err)
			}

			for _, service := range serviceMap {
				if service.Protocol == matcher.Protocol {
					foundService = service
					serviceApplication = matcher.Application
					break
				}
			}
		}
	}

	if foundService == nil || len(foundService.Id) == 0 {
		return nil, "", fmt.Errorf("no services found on protocol %s", matcher.Protocol)
	}

	return foundService, serviceApplication, nil
}

func (srv *Service) Stream(ctx context.Context, projectID, applicationID, protocol string) (iface.Stream, error) {
	if len(projectID) == 0 {
		return nil, errors.New("ProjectID is required")
	}

	matcher := &iface.MatchDefinition{
		Project:     projectID,
		Application: applicationID,
		Protocol:    protocol,
	}

	foundService, serviceApplication, err := srv.LookupService(matcher)
	if err != nil {
		return nil, err
	}

	return stream.New(srv, ctx, foundService, serviceApplication, matcher)
}
