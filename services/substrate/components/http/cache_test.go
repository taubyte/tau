package http

import (
	"context"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/http/common"
)

// TODO: Revisit cache clearing
func TestCache(t *testing.T) {
	t.Skip("cache needs to updated")
	s := NewTestService(peer.MockNode(context.Background()))
	testDomainName := "someDomain"
	testFunctionId := "someFuncId"
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
			Paths:   []string{"/ping", "/ping2"},
			Method:  "GET",
			Timeout: 100000,
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
	host := "hal.computers.com"

	functionMatcher := common.New(host, "/ping", "GET")
	functionMatcher2 := common.New(host, "/ping2", "GET")
	websiteMatcher := common.New(host, "/", "GET")

	if cached, _ := s.cache.Get(functionMatcher2, components.GetOptions{Validation: true}); len(cached) != 0 {
		t.Error("Function 2 should not be cached yet")
		return
	}

	if cached, _ := s.cache.Get(functionMatcher, components.GetOptions{Validation: true}); len(cached) != 0 {
		t.Error("Function should not be cached yet")
		return
	}

	if cached, _ := s.cache.Get(websiteMatcher, components.GetOptions{Validation: true}); len(cached) != 0 {
		t.Error("Website should not be cached yet")
		return
	}

	if err := checkFunction(s, testFunctionId, functionMatcher); err != nil {
		t.Error(err)
		return
	}

	if cached, _ := s.cache.Get(functionMatcher, components.GetOptions{Validation: true}); len(cached) != 1 {
		t.Error("Expected function to be cached")
		return
	}

	time.Sleep(500 * time.Microsecond)

	if cached, _ := s.cache.Get(functionMatcher, components.GetOptions{Validation: true}); len(cached) != 0 {
		t.Error("Expected function cache to be clear")
		return
	}

	if err := checkFunction(s, testFunctionId, functionMatcher); err != nil {
		t.Error(err)
		return
	}

	if err := checkFunction(s, testFunctionId, functionMatcher2); err != nil {
		t.Error(err)
		return
	}

	if cached, _ := s.cache.Get(functionMatcher, components.GetOptions{Validation: true}); len(cached) != 1 {
		t.Error("Expected function to be cached")
		return
	}

	if cached, _ := s.cache.Get(functionMatcher2, components.GetOptions{Validation: true}); len(cached) != 1 {
		t.Error("Expected function 2 to be cached")
		return
	}

	time.Sleep(200 * time.Microsecond)

	if cached, _ := s.cache.Get(functionMatcher, components.GetOptions{Validation: true}); len(cached) != 0 {
		t.Error("Expected function to be cleared")
		return
	}

	if cached, _ := s.cache.Get(functionMatcher2, components.GetOptions{Validation: true}); len(cached) != 0 {
		t.Error("Expected function 2 to be cleared")
		return
	}
}
