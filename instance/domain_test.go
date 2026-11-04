package instance

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type memVault struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (v *memVault) Get(_ context.Context, n string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	b, ok := v.m[n]
	if !ok {
		return nil, errors.New("not set")
	}
	return b, nil
}

func (v *memVault) Set(_ context.Context, n string, b []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.m[n] = b
	return nil
}

type noTXT struct{}

func (noTXT) LookupTXT(context.Context, string) ([]string, error) { return nil, errors.New("none") }

func TestAResolverIsTheOperatorsChoice(t *testing.T) {
	require.Error(t, WithResolver(nil)(&options{}))
	var o options
	require.NoError(t, WithResolver(noTXT{})(&o))
	require.NotNil(t, o.resolver)
}

func TestTheDomainsClientIsBuiltOnce(t *testing.T) {
	n := &Instance{Node: mockPeer(t)}
	built, err := n.domains()
	require.NoError(t, err)
	again, err := n.domains()
	require.NoError(t, err)
	require.Same(t, built, again)
	require.NoError(t, built.Close())
}
