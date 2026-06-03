package website

import (
	"testing"

	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

func TestSSREngineRegistry(t *testing.T) {
	// The wazero-backed engines must be registered.
	for _, abi := range []string{websiteSpec.ABIFunction, websiteSpec.ABIWasiStdio} {
		if _, ok := ssrEngines[abi]; !ok {
			t.Errorf("engine for abi %q not registered", abi)
		}
	}

	// The component engine is a declared slot but not yet backed in this build —
	// serving it must fail fast, not silently mishandle.
	if _, ok := ssrEngines[websiteSpec.ABIComponent]; ok {
		t.Error("component engine should not be registered until a backend exists")
	}

	supported := supportedSSRABIs()
	if len(supported) != 2 || supported[0] != websiteSpec.ABIFunction || supported[1] != websiteSpec.ABIWasiStdio {
		t.Errorf("supportedSSRABIs() = %v, want [function wasi-stdio] (sorted)", supported)
	}
}
