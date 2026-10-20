//go:build dreaming

package dream_test

import (
	"crypto/tls"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/services/globals"
)

// certificate_test issues one certificate for a fleet: two globals nodes and
// a frontend-only node that runs neither, against pebble in process, whose
// validation authority resolves through the fleet's own dns — DNS-01, the
// default. Every node presents the same leaf, which is the proof the name was
// ordered once for the cloud and not once per node.
func TestAServiceNameIsIssuedOnceForTheFleet(t *testing.T) {
	const cloud = "tau.dream.test"
	t.Setenv("PEBBLE_VA_NOSLEEP", "1")
	t.Setenv("PEBBLE_WFE_NONCEREJECT", "0")
	t.Setenv("PEBBLE_AUTHZREUSE", "0")

	logger := log.New(io.Discard, "", 0)
	store := db.NewMemoryStore()
	authority := ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "default"}})
	// THE VALIDATION AUTHORITY IS BUILT ONCE THE FLEET IS UP, because it must
	// be told the fleet's dns; until then the directory answers 503.
	var handler atomic.Pointer[http.Handler]
	directory := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := handler.Load()
		if h == nil {
			http.Error(w, "not yet", http.StatusServiceUnavailable)
			return
		}
		(*h).ServeHTTP(w, r)
	}))
	t.Cleanup(directory.Close)

	cfg := globals.Config{Certificate: globals.CertificateConfig{ACME: certificate.ACME{
		Enabled:   true,
		Directory: directory.URL + "/dir",
		RootCA:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: directory.Certificate().Raw})),
	}}}
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Copies: 3,
		Configure: func(index int, c *node.Config) {
			c.Gateway.CloudFqdn = cloud
			c.Globals = cfg
			switch index {
			case 0, 1:
				c.Services = append(slices.Clone(node.All), node.Globals)
			default:
				c.Services = []string{node.Discovery, node.BuildFrontend}
				c.BuildFrontend.Listen = "127.0.0.1:0"
			}
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	nodes := make([]*node.Node, 0, 3)
	for _, pid := range u.Pids() {
		nodes = append(nodes, nodeOf(t, pid))
	}
	var resolver string
	for _, n := range nodes {
		for _, a := range n.DNSAddrs() {
			if tcp, ok := a.(*net.TCPAddr); ok && resolver == "" {
				resolver = tcp.String()
			}
		}
	}
	require.NotEmpty(t, resolver, "a dns node serves tcp")
	v := va.New(logger, 1, 0, false, resolver, store)
	front := wfe.New(logger, store, v, authority, nil, false, false, 0, 0)
	h := front.Handler()
	handler.Store(&h)

	name := node.BuildFrontend + "." + cloud
	var leaves [][]byte
	for _, n := range nodes {
		secure, err := n.ServiceTLS()
		require.NoError(t, err)
		require.NotNil(t, secure)
		var cert *tls.Certificate
		require.Eventually(t, func() bool {
			cert, err = secure.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
			if err != nil {
				t.Logf("not yet: %v", err)
			}
			return err == nil
		}, 3*time.Minute, time.Second, "every node is answered, from the frontend that runs no globals to the globals nodes")
		require.Equal(t, []string{name}, cert.Leaf.DNSNames)
		leaves = append(leaves, cert.Certificate[0])
	}
	for _, leaf := range leaves[1:] {
		require.Equal(t, leaves[0], leaf, "one issuance for the cloud")
	}
}
