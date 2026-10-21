package certificate

// ACME is how a cloud issues certificates for its own zone's names, as an
// operator writes it. It is data and nothing else: pkg/certificate/acme builds
// the client from it.
//
// THE ZERO VALUE ISSUES NOTHING. A node is given a CA on purpose — issuing from
// Let's Encrypt is an agreement to its terms, and from anything else is a
// trust decision — so only Enabled turns issuance on.
type ACME struct {
	// Enabled issues a certificate for a generated name the operator's
	// wildcard does not cover.
	Enabled bool `yaml:"enabled"`

	// Directory is the CA's ACME directory URL. Empty is Let's Encrypt's
	// production directory; anything else is a custom CA (v1's custom-acme
	// switch was "a directory was set").
	Directory string `yaml:"directory"`

	// Email is the account's contact, optional.
	Email string `yaml:"email"`

	// AccountKey is the ACME account's private key, PEM (PKCS#1, PKCS#8 or
	// SEC 1). Empty, services/cm uses the cloud's one account, kept in
	// the secrets service and derived from the swarm key.
	//
	// EVERY NODE OF A CLOUD HOLDS THE SAME ONE. The HTTP-01 answer is derived
	// from the account key alone, so any node a CA reaches answers any node's
	// challenge.
	AccountKey string `yaml:"accountkey"`

	// RootCA is a PEM bundle trusted for the directory's TLS, beside the
	// system's. For a private CA.
	RootCA string `yaml:"rootca"`

	// InsecureSkipVerify does not verify the directory's TLS at all. For a
	// test CA only.
	InsecureSkipVerify bool `yaml:"insecureskipverify"`

	// EAB is the external account binding a commercial CA hands out to tie
	// the ACME account to an account it holds. Both halves or neither.
	EAB EAB `yaml:"eab"`
}

// EAB is an external account binding: the key id and the HMAC key the CA
// issued, the key base64url as CAs print it.
type EAB struct {
	KeyID string `yaml:"kid"`
	HMAC  string `yaml:"hmac"`
}

// LetsEncrypt is the production directory an enabled ACME with none uses.
const LetsEncrypt = "https://acme-v02.api.letsencrypt.org/directory"
