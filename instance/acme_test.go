package instance

import (
	"crypto/tls"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/certificate/acme"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/plm/ingress"
)

func TestACloudIssuingCertificatesNeedsItsZoneAndAWorkableConfig(t *testing.T) {
	on := cm.Config{Certificate: cm.CertificateConfig{ACME: certificate.ACME{Enabled: true}}}

	// A GATEWAY with no zone cannot say what it issues, and cm keeps nothing for it.
	_, err := New(mockPeer(t), rootedIn(t, Config{Services: []string{Register, Stats, Gateway}, CM: on}, t.TempDir()), withKVDB(&memFactory{}))
	require.ErrorIs(t, err, ErrNoZoneForCertificates)
	keeper, err := New(mockPeer(t), rootedIn(t, Config{Services: []string{Stats, Secrets, CM}, CM: on}, t.TempDir()), withKVDB(&memFactory{}))
	require.NoError(t, err)
	t.Cleanup(func() { keeper.Close() })
	require.ErrorIs(t, keeper.Start(t.Context()), cm.ErrNoZone)

	cfg := Config{Services: []string{Register, Stats, Secrets, Gateway, DNS, CM}, CM: on, CloudFqdn: "tau.example.io"}
	n, _, _ := makeNode(t, cfg)
	require.NotNil(t, n.certs, "its gateway and dns server take the client")
	require.NoError(t, n.Start(t.Context()))
	require.NotNil(t, cm.On(&added{node: n}).Store(), "a cm node hosts the store")

	cfg.CM.Certificate.ACME.EAB.KeyID = "half"
	_, err = New(mockPeer(t), rootedIn(t, cfg, t.TempDir()), withKVDB(&memFactory{}))
	require.ErrorContains(t, err, "external account binding")
}

func accountKey(t *testing.T) string {
	t.Helper()
	_, blob, err := acme.MintKey()
	require.NoError(t, err)
	return string(blob)
}

func TestBuildServesTLSWhenTheCloudIssues(t *testing.T) {
	n, _, _ := makeNode(t, Config{
		Services:  []string{Stats, PLM},
		Sections:  map[string]any{PLM: ingress.Config{Listen: "127.0.0.1:0"}},
		CloudFqdn: "tau.example.io",
		CM:        cm.Config{Certificate: cm.CertificateConfig{ACME: certificate.ACME{Enabled: true}}},
	})
	require.NoError(t, n.Start(t.Context()))

	res, err := http.Get("http://" + n.PLMAddr().String() + "/")
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Contains(t, string(body), "HTTPS server")

	_, err = tls.Dial("tcp", n.PLMAddr().String(), &tls.Config{ServerName: "build.tau.example.io"})
	require.Error(t, err, "no cm node keeps one, so nothing is presented")

	secure, err := n.ServiceTLS()
	require.NoError(t, err)
	require.NotNil(t, secure.GetCertificate)

	// A CLOUD THAT ISSUES NOTHING serves its frontend as it did.
	plain, _, _ := makeNode(t, Config{Services: []string{Stats, PLM}, Sections: map[string]any{PLM: ingress.Config{Listen: "127.0.0.1:0"}}})
	secure, err = plain.ServiceTLS()
	require.NoError(t, err)
	require.Nil(t, secure)
}
