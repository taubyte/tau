package dream

import (
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

// A service registered from an init() may not be pre-seeded from
// commonSpecs.Services, so Registry.Set has to admit an unknown protocol.
func TestRegistrySetLazySlot(t *testing.T) {
	const name = "unseeded-fake-service"
	if _, ok := Registry.registry[name]; ok {
		t.Skip("name unexpectedly pre-seeded")
	}
	t.Cleanup(func() { delete(Registry.registry, name) })

	create := func(*Universe, *commonIface.ServiceConfig) (commonIface.Service, error) {
		return nil, nil
	}
	if err := Registry.Set(name, create, nil); err != nil {
		t.Fatalf("Set on an unseeded protocol: %v", err)
	}
	if got, err := Registry.service(name); err != nil || got == nil {
		t.Fatalf("service() after Set: got=%v err=%v", got, err)
	}

	if err := Registry.Set("", create, nil); err == nil {
		t.Fatal("blank protocol should be refused")
	}
}

// provided() reflect-calls u.<Name>(); a registered service need not expose such
// an accessor, so a method-less name must return false, not panic.
func TestProvidedWithoutAccessor(t *testing.T) {
	const name = "faketestsvc"
	orig := append([]string(nil), commonSpecs.Services...)
	commonSpecs.Services = append(commonSpecs.Services, name)
	t.Cleanup(func() { commonSpecs.Services = orig })

	u := &Universe{}
	if u.provided(name) {
		t.Fatal("a service with no typed accessor should not be reported provided")
	}
}
