package forge

// signin.go is the optional face a forge has when a person can sign in to it
// through an OAuth app: where to send them, where to redeem the code, and what
// to ask for.
//
// IT IS THE DRIVER'S, NOT A SESSION'S, because there is no caller credential
// yet — signing in is how one is obtained. The face only NAMES endpoints; the
// exchange itself is OAuth 2.0 and is the same on every forge, so it is
// written once by whoever redeems a code, never per driver. Who the resulting
// credential is stays the mandatory Whoami, asked of a Session bound to it.
//
// The endpoints are derived from the host the driver was built for, which is
// the package's doctrine applied once more: a sign-in cannot be aimed at a
// server other than the forge it is for.

import "slices"

// CapSignIn is the sign-in face.
const CapSignIn Capability = "sign-in"

// OAuth is how a person signs in to one forge.
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

// SignIn is implemented by a Driver whose forge is an OAuth provider.
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
