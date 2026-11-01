package forge

// The sign-in face names a forge's OAuth endpoints, derived from the driver's
// host so a sign-in cannot be aimed elsewhere. It is the driver's, since there
// is no caller credential yet; the OAuth exchange itself is written once by
// whoever redeems a code.

import "slices"

const CapSignIn Capability = "sign-in"

type OAuth struct {
	// AuthorizeURL is where a browser is sent.
	AuthorizeURL string
	// TokenURL redeems a code, a device code or a refresh token.
	TokenURL string
	// DeviceURL starts the device flow (RFC 8628); empty is none.
	DeviceURL string
	// Scopes are what a sign-in asks for: enough to say who the person is
	// and to act on their repositories, which is what the token the client
	// is handed is then used for.
	Scopes []string
	// PKCE says the forge honours an S256 code challenge (RFC 7636).
	PKCE bool
	// SecretInBody sends the client secret as form parameters rather than
	// HTTP basic authentication.
	SecretInBody bool
}

type SignIn interface {
	OAuth() OAuth
}

// OAuth returns host's sign-in endpoints, or *UnsupportedError when its forge
// is not an OAuth provider — never a zero OAuth a caller would send a person
// to.
func (r *Registry) OAuth(host string) (OAuth, error) {
	d, err := r.Driver(host)
	if err != nil {
		return OAuth{}, err
	}
	s, ok := d.(SignIn)
	if !ok {
		return OAuth{}, &UnsupportedError{Host: host, Capability: CapSignIn}
	}
	o := s.OAuth()
	o.Scopes = slices.Clone(o.Scopes)
	return o, nil
}
