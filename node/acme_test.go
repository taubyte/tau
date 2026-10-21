package node

import (
	"crypto/tls"
	"io"
	"net/http"
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/cm"
	gateway "github.com/taubyte/tau/services/gateway"
)

func TestACloudIssuingCertificatesNeedsItsZoneAndAWorkableConfig(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	on := cm.Config{Certificate: cm.CertificateConfig{ACME: certificate.ACME{Enabled: true}}}

	// A GATEWAY OR A GLOBALS NODE with no zone cannot say what it issues.
	for _, services := range [][]string{{Register, Discovery, Gateway}, {Discovery, Secrets, CM}} {
		h := newHost(t, mn)
		_, err := New(Config{Services: services, CM: on}, WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}), WithRoot(t.TempDir()))
		require.ErrorIs(t, err, ErrNoZoneForCertificates, services)
	}

	cfg := Config{Services: []string{Register, Discovery, Secrets, Gateway, DNS, CM}, CM: on, Gateway: gateway.Config{CloudFqdn: "tau.example.io"}}
	n, _, _ := makeNode(t, cfg)
	require.NotNil(t, n.cm, "a cm node hosts the store")
	require.NotNil(t, n.certs, "and its gateway and dns server take the client")

	cfg.CM.Certificate.ACME.EAB.KeyID = "half"
	h := newHost(t, mn)
	_, err := New(cfg, WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}), WithRoot(t.TempDir()))
	require.ErrorContains(t, err, "external account binding")

	// A GLOBALS NODE KEEPS ITS CHAINS IN A DATABASE.
	h = newHost(t, mn)
	_, err = New(Config{Services: []string{Discovery, CM}, CM: on, Gateway: gateway.Config{CloudFqdn: "tau.example.io"}}, WithHost(h), WithPubSub(newPubSub(t, h)))
	require.ErrorIs(t, err, ErrNoDatabases)
}

func accountKey(t *testing.T) string {
	t.Helper()
	_, blob, err := acme.MintKey()
	require.NoError(t, err)
	return string(blob)
}

// THE BUILD FRONTEND SERVES TLS in a cloud that issues, so a forge's hook
// reaches https://build-frontend.<cloud fqdn>. With no cm node to keep a
// certificate in, the handshake fails, which is the proof it was a handshake and not plain HTTP.
func TestTheBuildFrontendServesTLSWhenTheCloudIssues(t *testing.T) {
	n, _, _ := makeNode(t, Config{
		Services:      []string{Discovery, BuildFrontend},
		BuildFrontend: frontend.Config{Listen: "127.0.0.1:0"},
		Gateway:       gateway.Config{CloudFqdn: "tau.example.io"},
		CM:            cm.Config{Certificate: cm.CertificateConfig{ACME: certificate.ACME{Enabled: true}}},
	})
	require.NoError(t, n.Start(t.Context()))

	res, err := http.Get("http://" + n.BuildFrontendAddr().String() + "/")
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Contains(t, string(body), "HTTPS server")

	_, err = tls.Dial("tcp", n.BuildFrontendAddr().String(), &tls.Config{ServerName: "build-frontend.tau.example.io"})
	require.Error(t, err, "no cm node keeps one, so nothing is presented")

	secure, err := n.ServiceTLS()
	require.NoError(t, err)
	require.NotNil(t, secure.GetCertificate)

	// A CLOUD THAT ISSUES NOTHING serves its frontend as it did.
	plain, _, _ := makeNode(t, Config{Services: []string{Discovery, BuildFrontend}, BuildFrontend: frontend.Config{Listen: "127.0.0.1:0"}})
	secure, err = plain.ServiceTLS()
	require.NoError(t, err)
	require.Nil(t, secure)
}

// A DNS NODE WITH NO DATABASES holds no copy of the challenge store, so it
// cannot answer DNS-01 and says so instead of answering empty.
func TestADNSNodeOfAnIssuingCloudNeedsTheChallengeStore(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	_, err := New(Config{
		Services: []string{Discovery, DNS},
		Gateway:  gateway.Config{CloudFqdn: "tau.example.io"},
		CM:       cm.Config{Certificate: cm.CertificateConfig{ACME: certificate.ACME{Enabled: true}}},
	}, WithHost(h), WithPubSub(newPubSub(t, h)), WithRoot(t.TempDir()))
	require.ErrorIs(t, err, ErrNoDatabases)
}
