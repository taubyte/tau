package auth

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/taubyte/tau/services/auth/password"
)

// Config is auth's operator configuration. Every secret it refers to is in
// the secrets service, never here; see AdminSecret, OAuthSecretName and
// OIDCSecretName.
type Config struct {
	// Issuer names this cloud in every token it signs, e.g. "tau:example.com".
	// A token from another cloud does not verify here.
	Issuer string

	Admin Admin

	// SignUp says who may become a user by signing in. The default is
	// SignUpInvite.
	SignUp SignUp

	// Redirects are the console callback URLs a browser sign-in may return
	// to, each matched exactly. A loopback address on any port at /callback
	// is always accepted, for the CLI (RFC 8252 §7.3).
	Redirects []string

	// Forges are this cloud's OAuth apps, one per forge host. The client
	// secret of each is in the vault under OAuthSecretName(host).
	Forges []ForgeApp

	// OIDC are bring-your-own OpenID Connect providers. The client secret of
	// each is in the vault under OIDCSecretName(name).
	OIDC []OIDCProvider

	// AccessTTL, RefreshTTL and InviteTTL default to DefaultAccessTTL,
	// DefaultRefreshTTL and DefaultInviteTTL.
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	InviteTTL  time.Duration

	// Params are the Argon2id costs new hashes are made with. Zero is
	// password.Default.
	Params password.Params
}

// Admin is the administrator named in configuration. It is never written to
// the store: every node reads it from its own configuration, so there is no
// first-user race and no record two nodes could write differently.
type Admin struct {
	// Name is what the admin signs in as. Empty is no built-in admin. The
	// password's hash is in the vault under AdminSecret.
	Name string
	// Disabled turns the admin off, once provider sign-in works.
	Disabled bool
}

// HashAdminPassword is what the vault holds under AdminSecret for pw. It
// carries no pepper: a pepper is per node, and every node verifies this hash.
func HashAdminPassword(pw string) (string, error) {
	h, err := password.New(pw, "", nil, password.Default)
	if err != nil {
		return "", err
	}
	return h.Encode()
}

type SignUp string

const (
	// SignUpInvite admits a first sign-in only with an invite.
	SignUpInvite SignUp = "invite"
	// SignUpOpen admits anyone a provider vouches for.
	SignUpOpen SignUp = "open"
)

type ForgeApp struct {
	Host     string
	ClientID string
}

type OIDCProvider struct {
	// Name is how a client names it, and the vault name of its secret.
	Name     string
	Issuer   string
	ClientID string
	// Scopes beyond openid; default email and profile.
	Scopes []string
}

const (
	DefaultAccessTTL  = 15 * time.Minute
	DefaultRefreshTTL = 24 * time.Hour
	DefaultInviteTTL  = 7 * 24 * time.Hour
)

func (c *Config) defaults() error {
	if c.Issuer == "" {
		return errors.New("auth: no issuer")
	}
	switch c.SignUp {
	case "":
		c.SignUp = SignUpInvite
	case SignUpInvite, SignUpOpen:
	default:
		return fmt.Errorf("auth: sign-up %q is neither %q nor %q", c.SignUp, SignUpInvite, SignUpOpen)
	}
	if c.AccessTTL == 0 {
		c.AccessTTL = DefaultAccessTTL
	}
	if c.RefreshTTL == 0 {
		c.RefreshTTL = DefaultRefreshTTL
	}
	if c.InviteTTL == 0 {
		c.InviteTTL = DefaultInviteTTL
	}
	if c.Params == (password.Params{}) {
		c.Params = password.Default
	}
	for _, r := range c.Redirects {
		u, err := url.Parse(r)
		if err != nil || u.Scheme != "https" && !isLoopback(u) || u.Host == "" || u.Fragment != "" {
			return fmt.Errorf("auth: redirect %q is not an absolute https URL", r)
		}
	}
	var names []string
	for _, f := range c.Forges {
		if f.Host == "" || f.ClientID == "" {
			return errors.New("auth: a forge app needs a host and a client id")
		}
		names = append(names, f.Host)
	}
	for _, o := range c.OIDC {
		if o.Name == "" || o.Issuer == "" || o.ClientID == "" {
			return errors.New("auth: an OIDC provider needs a name, an issuer and a client id")
		}
		names = append(names, o.Name)
	}
	slices.Sort(names)
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			return fmt.Errorf("auth: two sign-in providers are named %q", names[i])
		}
	}
	return nil
}
