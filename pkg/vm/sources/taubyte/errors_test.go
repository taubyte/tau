package source

import (
	"errors"
	"strings"
	"testing"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm/loaders"
	"github.com/taubyte/tau/pkg/vm/test_utils"
)

var errNoSuchModule = errors.New("no such module")

type failingResolver struct{}

func (failingResolver) Lookup(vm.Context, string) (ma.Multiaddr, error) { return nil, errNoSuchModule }

// Structural, not wording: each layer adds only its own step, so no 3-word
// phrase repeats along the chain, and the cause stays reachable.
func TestModuleErrorSaysEachStepOnce(t *testing.T) {
	ctx, err := test_utils.Context()
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(loaders.New(failingResolver{})).Module(ctx, "functions/hello")
	if !errors.Is(err, errNoSuchModule) {
		t.Fatalf("cause not wrapped: %v", err)
	}
	words := strings.Fields(strings.ToLower(err.Error()))
	for i := range words {
		words[i] = strings.Trim(words[i], "`:\"\x27")
	}
	seen := map[string]bool{}
	for i := 0; i+3 <= len(words); i++ {
		p := strings.Join(words[i:i+3], " ")
		if seen[p] {
			t.Fatalf("%q repeats in %q", p, err)
		}
		seen[p] = true
	}
}
