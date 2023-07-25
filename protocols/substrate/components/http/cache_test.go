package http

import (
	"testing"
	"time"

	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/substrate/components/http/common"
)

// TODO: Revisit cache clearing
func TestCache(t *testing.T) {
	t.Skip("cache needs to updated")
	s := NewTestService(nil)
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

	if cached, _ := s.cache.Get(functionMatcher2); len(cached) != 0 {
		t.Error("Function 2 should not be cached yet")
		return
	}

	if cached, _ := s.cache.Get(functionMatcher); len(cached) != 0 {
		t.Error("Function should not be cached yet")
		return
	}

	if cached, _ := s.cache.Get(websiteMatcher); len(cached) != 0 {
		t.Error("Website should not be cached yet")
		return
	}

	if err := checkFunction(s, testFunctionId, functionMatcher); err != nil {
		t.Error(err)
		return
	}

	if cached, _ := s.cache.Get(functionMatcher); len(cached) != 1 {
		t.Error("Expected function to be cached")
		return
	}

	// time.Sleep(500 * time.Microsecond)

	// if cached, _ := s.cache.Get(functionMatcher); len(cached) != 0 {
	// 	t.Error("Expected function cache to be clear")
	// 	return
	// }

	if err := checkFunction(s, testFunctionId, functionMatcher); err != nil {
		t.Error(err)
		return
	}

	if err := checkFunction(s, testFunctionId, functionMatcher2); err != nil {
		t.Error(err)
		return
	}

	if cached, _ := s.cache.Get(functionMatcher); len(cached) != 1 {
		t.Error("Expected function to be cached")
		return
	}

	if cached, _ := s.cache.Get(functionMatcher2); len(cached) != 1 {
		t.Error("Expected function 2 to be cached")
		return
	}

	time.Sleep(200 * time.Microsecond)

	// if cached, _ := s.cache.Get(functionMatcher); len(cached) != 0 {
	// 	t.Error("Expected function to be cleared")
	// 	return
	// }

	// if cached, _ := s.cache.Get(functionMatcher2); len(cached) != 0 {
	// 	t.Error("Expected function 2 to be cleared")
	// 	return
	// }
}
