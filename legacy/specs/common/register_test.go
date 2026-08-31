package common

import (
	"testing"

	"golang.org/x/exp/slices"
)

func TestRegisterService(t *testing.T) {
	// RegisterService mutates package-global slices; snapshot and restore them
	// so sibling tests never see the fake service.
	snap := func() (a, b, c, d []string) {
		return append([]string(nil), Services...),
			append([]string(nil), HTTPServices...),
			append([]string(nil), P2PStreamServices...),
			append([]string(nil), Clients...)
	}
	oServices, oHTTP, oP2P, oClients := snap()
	t.Cleanup(func() {
		Services, HTTPServices, P2PStreamServices, Clients = oServices, oHTTP, oP2P, oClients
	})

	const name = "fake-test-service"
	RegisterService(name, ServiceCapabilities{HTTP: true, Client: true})

	if !slices.Contains(Services, name) {
		t.Fatal("name not added to Services")
	}
	if !slices.Contains(HTTPServices, name) {
		t.Fatal("HTTP:true not reflected in HTTPServices")
	}
	if !slices.Contains(Clients, name) {
		t.Fatal("Client:true not reflected in Clients")
	}
	if slices.Contains(P2PStreamServices, name) {
		t.Fatal("P2PStream was false but name is in P2PStreamServices")
	}

	// Idempotent: a repeat call must neither append again nor reclassify.
	before := len(Services)
	RegisterService(name, ServiceCapabilities{P2PStream: true})
	if len(Services) != before {
		t.Fatal("duplicate RegisterService appended again")
	}
	if slices.Contains(P2PStreamServices, name) {
		t.Fatal("idempotent call reclassified an existing service")
	}

	// Blank name is a no-op.
	RegisterService("", ServiceCapabilities{})
	if slices.Contains(Services, "") {
		t.Fatal("blank name was registered")
	}
}
