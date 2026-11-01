package certificate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func pair(t *testing.T, name string) Pair {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{name},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &private.PublicKey, private)
	require.NoError(t, err)
	marshalled, err := x509.MarshalECPrivateKey(private)
	require.NoError(t, err)
	return Pair{
		Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Key:         string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: marshalled})),
	}
}

func TestTheWildcardCoversTheZoneAndNothingElse(t *testing.T) {
	p := pair(t, "*.shop.tau.example.io")
	require.True(t, p.Held())

	cert, err := Wildcard(p, "tau.example.io", "API.shop.TAU.example.io")
	require.NoError(t, err)
	require.NotEmpty(t, cert.Certificate)

	_, err = Wildcard(p, "tau.example.io", "shop.example.com")
	require.ErrorIs(t, err, ErrNotCovered)
	// The zone itself is not under the zone, nor is a name that only ends the same way.
	_, err = Wildcard(p, "tau.example.io", "tau.example.io")
	require.ErrorIs(t, err, ErrNotCovered)
	_, err = Wildcard(p, "tau.example.io", "eviltau.example.io")
	require.ErrorIs(t, err, ErrNotCovered)
	_, err = Wildcard(p, "", "a.tau.example.io")
	require.ErrorIs(t, err, ErrNotCovered, "a cloud that knows no zone of its own covers nothing")

	_, err = Wildcard(Pair{Certificate: p.Certificate}, "tau.example.io", "a.tau.example.io")
	require.ErrorIs(t, err, ErrNoWildcard)
}

func TestOneSecretHoldsBothHalves(t *testing.T) {
	p := pair(t, "shop.example.com")

	for _, blob := range []string{p.Certificate + p.Key, p.Key + p.Certificate} {
		cert, err := KeyPair([]byte(blob))
		require.NoError(t, err)
		require.NotEmpty(t, cert.Certificate)
	}

	_, err := KeyPair([]byte(p.Certificate))
	require.ErrorIs(t, err, ErrNoKey)
	_, err = KeyPair([]byte(p.Key))
	require.ErrorIs(t, err, ErrNoChain)
	_, err = KeyPair([]byte("not pem at all"))
	require.ErrorIs(t, err, ErrNoChain)

	other := pair(t, "other.example.com")
	_, err = KeyPair([]byte(p.Certificate + other.Key))
	require.Error(t, err, "a key that is not the chain's is no certificate")
}
