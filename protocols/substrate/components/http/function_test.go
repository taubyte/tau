package http

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/protocols/substrate/components/structure"
)

func TestFunction(t *testing.T) {
	t.Skip("This test needs to be updated")
	s := NewTestService(peer.MockNode(context.Background()))
	testDomainName := "someDomain"
	testFunctionId := "someFuncId"
	testFunctionName := "someFunctionName"

	domains = map[string]structureSpec.Domain{
		"someDomainId": {
			Name: testDomainName,
			Fqdn: "hal.computers.com",
		},
	}

	websites = nil
	functions = map[string]structureSpec.Function{
		testFunctionId: {
			Name: testFunctionName,
			Id:   testFunctionId,
			Domains: []string{
				testDomainName,
			},
			Paths:   []string{"/"},
			Method:  "GET",
			Timeout: 100000000000000000,
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

	var w http.ResponseWriter
	r := &http.Request{
		Host: "hal.computers.com",
		URL: &url.URL{
			Path: "/",
		},
		Method: "GET",
	}

	err = s.Handle(w, r)
	if err != nil {
		t.Error(err)
		return
	}

	if structure.CheckAttached(t, map[string]int{
		"functions/" + testFunctionName: 1,
	}) == false {
		return
	}

	e := structure.CalledTestFunctionsHttp[0]
	if e.R != r {
		t.Errorf("Got request: %v, expected: %v", e.R, r)
	}

	if e.W != w {
		t.Errorf("Got writer: %v, expected: %v", e.W, w)
	}
}
