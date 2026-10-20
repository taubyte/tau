package node

import (
	"crypto/tls"
	"io"
	"net/http"
	"testing"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/services/build/frontend"
	certificateService "github.com/taubyte/tau/services/certificate"
	gateway "github.com/taubyte/tau/services/gateway"
)

func TestACloudIssuingCertificatesNeedsItsZoneAndAWorkableConfig(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	on := certificateService.Config{ACME: certificate.ACME{Enabled: true}}

	// A GATEWAY OR A CERTIFICATE NODE with no zone cannot say what it issues.
	for _, services := range [][]string{{Register, Discovery, Gateway}, {Discovery, Secrets, Certificate}} {
		h := newHost(t, mn)
		_, err := New(Config{Services: services, Certificate: on}, WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}), WithRoot(t.TempDir()))
		require.ErrorIs(t, err, ErrNoZoneForCertificates, services)
	}

	cfg := Config{Services: []string{Register, Discovery, Secrets, Gateway, DNS, Certificate}, Certificate: on, Gateway: gateway.Config{CloudFqdn: "tau.example.io"}}
	n, _, _ := makeNode(t, cfg)
	require.NotNil(t, n.certificate, "a certificate node issues")
	require.NotNil(t, n.certs, "and its gateway and dns server take the client")

	cfg.Certificate.ACME.EAB.KeyID = "half"
	h := newHost(t, mn)
	_, err := New(cfg, WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}), WithRoot(t.TempDir()))
	require.ErrorContains(t, err, "external account binding")

	// A CERTIFICATE NODE KEEPS ITS CHAINS IN A DATABASE.
	h = newHost(t, mn)
	_, err = New(Config{Services: []string{Discovery, Certificate}, Certificate: on, Gateway: gateway.Config{CloudFqdn: "tau.example.io"}}, WithHost(h), WithPubSub(newPubSub(t, h)))
	require.ErrorIs(t, err, ErrNoDatabases)
}

// THE BUILD FRONTEND SERVES TLS in a cloud that issues, so a forge's hook
// reaches https://build-frontend.<cloud fqdn>. With no certificate node the
// handshake fails, which is the proof it was a handshake and not plain HTTP.
func TestTheBuildFrontendServesTLSWhenTheCloudIssues(t *testing.T) {
	n, _, _ := makeNode(t, Config{
		Services:      []string{Discovery, BuildFrontend},
		BuildFrontend: frontend.Config{Listen: "127.0.0.1:0"},
		Gateway:       gateway.Config{CloudFqdn: "tau.example.io"},
		Certificate:   certificateService.Config{ACME: certificate.ACME{Enabled: true}},
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
	require.Error(t, err, "no certificate node answers, so nothing is presented")

	secure, err := n.ServiceTLS()
	require.NoError(t, err)
	require.NotNil(t, secure.GetCertificate)

	// A CLOUD THAT ISSUES NOTHING serves its frontend as it did.
	plain, _, _ := makeNode(t, Config{Services: []string{Discovery, BuildFrontend}, BuildFrontend: frontend.Config{Listen: "127.0.0.1:0"}})
	secure, err = plain.ServiceTLS()
	require.NoError(t, err)
	require.Nil(t, secure)
}
