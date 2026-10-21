package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/certificate"
)

type fakeIssuer struct {
	cert  tls.Certificate
	err   error
	asked []string
}

func (f *fakeIssuer) Certificate(_ context.Context, name string) (tls.Certificate, error) {
	f.asked = append(f.asked, name)
	return f.cert, f.err
}

func (f *fakeIssuer) Challenge(token string) (string, bool) { return token + ".thumb", token != "bad" }

func TestAGeneratedNameTheWildcardDoesNotHoldIsIssued(t *testing.T) {
	name := generatedName(t)
	cert, key, _ := issued(t, name)
	pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
	require.NoError(t, err)
	issuer := &fakeIssuer{cert: pair}
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"}, WithIssuer(issuer))

	got, err := h.Gateway.Certificate(hello(name))
	require.NoError(t, err)
	require.Equal(t, cert, leafOf(t, got))
	require.Equal(t, []string{name}, issuer.asked)

	// A NAME OUTSIDE THE ZONE is never sent to the CA.
	_, err = h.Gateway.Certificate(hello("shop.example.com"))
	require.ErrorIs(t, err, ErrNoCertificate)
	require.Equal(t, []string{name}, issuer.asked)

	// An issuance that fails is the handshake's failure.
	issuer.err = errors.New("ca down")
	_, err = h.Gateway.Certificate(hello(name))
	require.ErrorContains(t, err, "ca down")
}

func TestTheWildcardWinsOverTheIssuerWhereItCovers(t *testing.T) {
	name := generatedName(t)
	cert, key, _ := issued(t, name)
	issuer := &fakeIssuer{}
	h := newHarness(t, Config{CloudFqdn: "tau.example.io", Wildcard: certificate.Pair{Certificate: cert, Key: key}}, WithIssuer(issuer))
	got, err := h.Gateway.Certificate(hello(name))
	require.NoError(t, err)
	require.Equal(t, cert, leafOf(t, got))
	require.Empty(t, issuer.asked)
}

func TestTheCAsChallengeIsAnsweredBeforeAnyName(t *testing.T) {
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"}, WithIssuer(&fakeIssuer{}))
	w := h.do(t, http.MethodGet, "http://nobody.example.com/.well-known/acme-challenge/tok", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "tok.thumb", w.Body.String())

	w = h.do(t, http.MethodGet, "http://nobody.example.com/.well-known/acme-challenge/bad", "")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	// Without an issuer the path is an ordinary one.
	plain := newHarness(t, Config{CloudFqdn: "tau.example.io"})
	w = plain.do(t, http.MethodGet, "http://nobody.example.com/.well-known/acme-challenge/tok", "")
	require.NotEqual(t, "tok.thumb", w.Body.String())

	require.Error(t, WithIssuer(nil)(&options{}))
}
