package node

import (
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/certificate"
	gateway "github.com/taubyte/tau/services/gateway"
)

func TestAGatewayIssuingByACMENeedsARootAndAWorkableConfig(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	cfg := Config{Services: []string{Register, Discovery, Gateway}, Gateway: gateway.Config{CloudFqdn: "tau.example.io", ACME: certificate.ACME{Enabled: true}}}

	_, err := New(cfg, WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}))
	require.ErrorContains(t, err, "node's root")

	n, _, _ := makeNode(t, cfg)
	require.NotNil(t, n, "with a root it builds")

	cfg.Gateway.ACME.EAB.KeyID = "half"
	h2 := newHost(t, mn)
	_, err = New(cfg, WithHost(h2), WithPubSub(newPubSub(t, h2)), WithDatabases(&memFactory{}), WithRoot(t.TempDir()))
	require.ErrorContains(t, err, "external account binding")
}
