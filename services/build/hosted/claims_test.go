package hosted

import (
	"context"
	"errors"
	"sync"
	"testing"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/peer"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/services/cm"
)

// memVault is cm's vault, in memory.
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

func TestTheClaimRouteSeamSaysWhatIsMalformed(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := p.Host()
	db, err := kvdbmock.New().New(nil, "cm", 0)
	require.NoError(t, err)
	svc, err := cm.Open(h, db, cm.Config{Certificate: cm.CertificateConfig{Zone: "tau.example"}}, cm.WithVault(&memVault{m: map[string][]byte{}}))
	require.NoError(t, err)

	d, err := cm.NewDomains(h, beside{svc})
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	c, err := claims{d}.Claim(t.Context(), "QmNTj7BY3YNySGaUrfuWjkomCME8fTcynvgHiNdX1hyLME", "shop.test")
	require.NoError(t, err)
	require.Equal(t, "QmNTj7BY.shop.test", c.Entry)
	require.Equal(t, "txt", c.Type)
	require.NotEmpty(t, c.Token)

	_, err = claims{d}.Claim(t.Context(), "not a cid", "shop.test")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	none, err := cm.NewDomains(h, beside{})
	require.NoError(t, err)
	_, err = claims{none}.Claim(t.Context(), "QmNTj7BY3YNySGaUrfuWjkomCME8fTcynvgHiNdX1hyLME", "shop.test")
	require.ErrorIs(t, err, cm.ErrNoMembers)
}

// beside is a cm store kept beside the caller, and no other cm node.
type beside struct{ store *cm.Store }

func (beside) Members() []libp2ppeer.ID { return nil }
func (b beside) Store() *cm.Store       { return b.store }
