package node

import (
	"context"
	"errors"
	"sync"
	"testing"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/services/cm"
)

type memVault struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (v *memVault) Get(_ context.Context, p, e, n string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	b, ok := v.m[p+e+n]
	if !ok {
		return nil, errors.New("not set")
	}
	return b, nil
}

func (v *memVault) Set(_ context.Context, p, e, n string, b []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.m[p+e+n] = b
	return nil
}

type noTXT struct{}

func (noTXT) LookupTXT(context.Context, string) ([]string, error) { return nil, errors.New("none") }

func TestADevelopmentCloudTrustsItsZones(t *testing.T) {
	n := &Node{}
	n.cfg.CM.Domains.Trusted = true
	p, err := n.domainProver()
	require.NoError(t, err)
	require.NoError(t, p.Prove(t.Context(), "anything", "example.com"))

	n.cfg.CM.Domains.Trusted = false
	_, err = n.domainProver()
	require.ErrorIs(t, err, cm.ErrNoHost, "without a host there is nothing to prove against")

	require.Error(t, WithResolver(nil)(&options{}))
	var o options
	require.NoError(t, WithResolver(noTXT{})(&o))
	require.NotNil(t, o.resolver)
}

func TestTheClaimRouteSeamSaysWhatIsMalformed(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h, err := mn.GenPeer()
	require.NoError(t, err)
	db, err := kvdbmock.New().New(nil, "cm", 0)
	require.NoError(t, err)
	svc, err := cm.New(h, db, cm.Config{Certificate: cm.CertificateConfig{Zone: "tau.example"}}, cm.WithVault(&memVault{m: map[string][]byte{}}))
	require.NoError(t, err)

	n := &Node{cm: svc}
	n.opts.host = h
	n.opts.resolver = noTXT{}
	d, err := cm.NewDomains(h, func() []libp2ppeer.ID { return nil }, func() *cm.Service { return n.cm })
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	c, err := domainClaims{d}.Claim(t.Context(), "QmNTj7BY3YNySGaUrfuWjkomCME8fTcynvgHiNdX1hyLME", "shop.test")
	require.NoError(t, err)
	require.Equal(t, "QmNTj7BY.shop.test", c.Entry)
	require.Equal(t, "txt", c.Type)
	require.NotEmpty(t, c.Token)

	_, err = domainClaims{d}.Claim(t.Context(), "not a cid", "shop.test")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	none, err := cm.NewDomains(h, func() []libp2ppeer.ID { return nil }, func() *cm.Service { return nil })
	require.NoError(t, err)
	_, err = domainClaims{none}.Claim(t.Context(), "QmNTj7BY3YNySGaUrfuWjkomCME8fTcynvgHiNdX1hyLME", "shop.test")
	require.ErrorIs(t, err, cm.ErrNoMembers)

	built, err := n.domains()
	require.NoError(t, err)
	again, err := n.domains()
	require.NoError(t, err)
	require.Same(t, built, again)
	require.NoError(t, built.Close())
}
