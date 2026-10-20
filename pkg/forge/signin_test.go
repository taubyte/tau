package forge

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type signing struct {
	bare
	scopes []string
}

func (d *signing) OAuth() OAuth {
	return OAuth{AuthorizeURL: "https://" + d.host + "/a", TokenURL: "https://" + d.host + "/t", Scopes: d.scopes}
}

func TestOAuthIsAFaceNeverAZeroValue(t *testing.T) {
	plain := &bare{host: "plain.example"}
	signs := &signing{bare: bare{host: "signs.example"}, scopes: []string{"repo"}}
	reg, err := New(Policy{}, Binding{Host: plain.host, Driver: plain}, Binding{Host: signs.host, Driver: signs})
	require.NoError(t, err)

	_, err = reg.OAuth(plain.host)
	var unsupported *UnsupportedError
	require.True(t, errors.As(err, &unsupported))
	require.Equal(t, CapSignIn, unsupported.Capability)

	_, err = reg.OAuth("nobody.example")
	var none *NoDriverError
	require.True(t, errors.As(err, &none))

	o, err := reg.OAuth(signs.host)
	require.NoError(t, err)
	require.Equal(t, "https://signs.example/t", o.TokenURL)
	// The scopes are a copy: a caller appending to them does not change the
	// driver's.
	o.Scopes[0] = "changed"
	o2, err := reg.OAuth(signs.host)
	require.NoError(t, err)
	require.Equal(t, []string{"repo"}, o2.Scopes)

	s, err := reg.Host(signs.host, Token("t"))
	require.NoError(t, err)
	require.True(t, s.Capabilities().Has(CapSignIn))
}
