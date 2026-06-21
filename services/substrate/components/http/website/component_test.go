package website

import (
	"context"
	goHttp "net/http"
	"testing"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

type fakeComponentRuntime struct{}

func (fakeComponentRuntime) Name() string { return "fake" }
func (fakeComponentRuntime) ServeHTTP(context.Context, string, []byte, goHttp.ResponseWriter, *goHttp.Request, ComponentLimits) error {
	return nil
}

func TestRegisterComponentRuntime(t *testing.T) {
	// By default no backend is registered and ABIComponent is not an engine.
	if _, ok := ssrEngines[websiteSpec.ABIComponent]; ok {
		t.Fatal("component engine registered before any backend")
	}

	// Registering a backend enables the component engine; clean up after.
	prev := componentRuntime
	t.Cleanup(func() {
		componentRuntime = prev
		delete(ssrEngines, websiteSpec.ABIComponent)
	})

	RegisterComponentRuntime(fakeComponentRuntime{})

	if componentRuntime == nil || componentRuntime.Name() != "fake" {
		t.Error("component runtime not registered")
	}
	if _, ok := ssrEngines[websiteSpec.ABIComponent]; !ok {
		t.Error("component engine not enabled after registration")
	}
	if !contains(supportedSSRABIs(), websiteSpec.ABIComponent) {
		t.Error("component abi should be reported as supported once a backend is registered")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
