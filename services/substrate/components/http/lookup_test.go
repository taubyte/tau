package http

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/taubyte/p2p/peer"
	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/components/http/function"
	"github.com/taubyte/tau/services/substrate/components/http/website"
	"github.com/taubyte/tau/services/substrate/runtime/lookup"
)

func TestLookup(t *testing.T) {
	s := NewTestService(peer.MockNode(context.Background()))
	testDomainName := "someDomain"
	testFunctionId := "someFuncId"
	testWebsiteId := "someWebId"

	domains = map[string]structureSpec.Domain{
		"someDomainId": {
			Name: testDomainName,
			Fqdn: "hal.computers.com",
		},
	}

	functions = map[string]structureSpec.Function{
		testFunctionId: {
			Id:   testFunctionId,
			Name: "someFunc",
			Domains: []string{
				testDomainName,
			},
			Paths:  []string{"/ping", "/ping2"},
			Method: "GET",
		},
	}
	websites = map[string]structureSpec.Website{
		testWebsiteId: {
			Id: testWebsiteId,
			Domains: []string{
				testDomainName,
			},
			Paths:    []string{"/"},
			Name:     "someWebsite",
			Branch:   "master",
			Provider: "github",
			RepoID:   "123",
			RepoName: "reponame",
		},
	}

	err := fakeFetch(s.Tns(),
		websites,
		functions,
		domains,
	)
	if err != nil {
		t.Error(err)
		return
	}

	// Success
	if err := checkFunction(s, testFunctionId, functionMatch1); err != nil {
		t.Error(err)
		return
	}

	if err := checkFunction(s, testFunctionId, functionMatch2); err != nil {
		t.Error(err)
		return
	}

	if err := checkWebsite(s, testWebsiteId, websiteMatch); err != nil {
		t.Error(err)
		return
	}

	// Failures
	if err := checkFunction(s, testFunctionId, functionNoMatch1); err == nil {
		t.Error("Expected error funcnomatch1")
		return
	}

	if err := checkFunction(s, testFunctionId, functionNoMatch2); err == nil {
		t.Error("Expected error funcnomatch2")
		return
	}

	if err := checkFunction(s, testFunctionId, functionNoMatch3); err == nil {
		t.Error("Expected error funcnomatch3")
		return
	}

	if err := checkFunction(s, testFunctionId, functionNoMatch4); err == nil {
		t.Error("Expected error funcnomatch4")
		return
	}
}

func checkFunction(s *Service, id string, matcher commonIface.MatchDefinition) error {

	picks, err := lookup.Lookup(s, matcher)

	if err != nil {
		return err
	}

	f, ok := picks[0].(*function.Function)
	if !ok {
		return errors.New("Not ok")
	}

	if !reflect.DeepEqual(functions[id], *f.Config()) {
		return fmt.Errorf("Expected: %#v, got: %#v", functions[id], f.Config())
	}

	return nil
}

// TODO:  Need to Have a Fake peer node with a fake get File that returns readable data
func checkWebsite(s *Service, id string, matcher *common.MatchDefinition) error {
	picks, err := lookup.Lookup(s, matcher)
	if err != nil {
		return err
	}

	w, ok := picks[0].(*website.Website)
	if !ok {
		return errors.New("Not ok")
	}

	if !reflect.DeepEqual(websites[id], *w.Config()) {
		return fmt.Errorf("Expected: %#v, got: %#v", websites[id], *w.Config())
	}

	return nil
}
