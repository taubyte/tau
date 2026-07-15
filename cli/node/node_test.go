package node

import (
	"context"
	"testing"

	"github.com/taubyte/tau/core/services"
	"github.com/taubyte/tau/pkg/config"
)

type fakePkg struct{}

func (fakePkg) New(context.Context, config.Config) (services.Service, error) { return nil, nil }

func TestRegister(t *testing.T) {
	const name = "fake-test-service"
	delete(available, name)
	t.Cleanup(func() { delete(available, name) })

	if err := Register(name, fakePkg{}); err != nil {
		t.Fatalf("registering a new service: %v", err)
	}
	if _, ok := available[name]; !ok {
		t.Fatal("service missing from registry after Register")
	}

	if err := Register(name, fakePkg{}); err == nil {
		t.Fatal("re-registering the same name should be refused")
	}
	if err := Register("auth", fakePkg{}); err == nil {
		t.Fatal("overwriting a built-in service should be refused")
	}
	if err := Register("", fakePkg{}); err == nil {
		t.Fatal("blank name should be refused")
	}

	// A refused registration must not have mutated the built-in entry.
	if available["auth"] == config.ProtoCommandIface(fakePkg{}) {
		t.Fatal("built-in auth was overwritten")
	}
}
