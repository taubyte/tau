package loaders_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/core/vm"
	loaders "github.com/taubyte/tau/pkg/vm/loaders"
	test "github.com/taubyte/tau/pkg/vm/test_utils"
)

type fixedResolver struct{ addr ma.Multiaddr }

func (r fixedResolver) Lookup(vm.Context, string) (ma.Multiaddr, error) { return r.addr, nil }

type failingBackend struct{ err error }

func (failingBackend) Scheme() string                            { return "dfs" }
func (b failingBackend) Get(ma.Multiaddr) (io.ReadCloser, error) { return nil, b.err }
func (failingBackend) Close() error                              { return nil }

// When no backend can fetch a module, the error says why and from where: it
// used to read "fetching module ... failed" and nothing else.
func TestLoadReportsWhyNoBackendFetched(t *testing.T) {
	addr, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/1")
	if err != nil {
		t.Fatal(err)
	}
	timeout := errors.New("getting file: context deadline exceeded")
	loader := loaders.New(fixedResolver{addr}, failingBackend{timeout}, failingBackend{errors.New("not url compliant")})

	ctx, err := test.Context()
	if err != nil {
		t.Fatal(err)
	}
	_, err = loader.Load(ctx, "functions/hello")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, timeout) {
		t.Fatalf("backend error not wrapped: %v", err)
	}
	for _, want := range []string{"functions/hello", addr.String(), "not url compliant"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%q missing from %q", want, err)
		}
	}
}
