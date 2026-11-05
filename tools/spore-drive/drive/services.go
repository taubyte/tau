package drive

import (
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/forge/drivers"

	"github.com/taubyte/tau/pkg/certificate"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/tools/spore-drive/config"
)

// cloudServices is what every node is told about sign-in and certificates,
// and secrets what of them is secret: the vault's.
type cloudServices struct {
	auth    authService.Config
	cm      cm.Config
	forges  []drivers.Forge
	secrets []cloudSecret
	// trusted serves every zone a project declares unproven: a builder's.
	trusted bool
}

type cloudSecret struct {
	ref   secrets.Ref
	value string
}

// bundle seals every cloud secret once. The same bundle goes to every host
// running the secrets service, so each writes the same version.
func (c *cloudServices) bundle(now time.Time) (*secretsv2.InitBundle, error) {
	out := &secretsv2.InitBundle{}
	for _, s := range c.secrets {
		sealed, err := secrets.Sealed(s.ref, []byte(s.value), now)
		if err != nil {
			return nil, err
		}
		out.Secrets = append(out.Secrets, sealed)
	}
	return out, nil
}

func servicesOf(p config.Parser) (*cloudServices, error) {
	a, err := p.Cloud().Auth()
	if err != nil {
		return nil, fmt.Errorf("cloud.auth: %w", err)
	}
	m, err := p.Cloud().CM()
	if err != nil {
		return nil, fmt.Errorf("cloud.cm: %w", err)
	}

	out := &cloudServices{}
	out.auth.SignUp = authService.SignUp(a.SignUp)
	out.auth.Redirects = a.Redirects
	secret := func(name, value string) {
		if value != "" {
			out.secrets = append(out.secrets, cloudSecret{ref: secrets.Ref{Scope: secrets.Cloud(authService.VaultService), Name: name}, value: value})
		}
	}
	if a.Admin.Name != "" {
		hash, err := authService.HashAdminPassword(a.Admin.Password)
		if err != nil {
			return nil, fmt.Errorf("cloud.auth.admin: %w", err)
		}
		out.auth.Admin = authService.Admin{Name: a.Admin.Name}
		secret(authService.AdminSecret, hash)
	}
	for _, f := range a.Forges {
		if f.Driver != "" {
			out.forges = append(out.forges, drivers.Forge{Host: f.Host, Driver: f.Driver})
		}
		if f.ClientID != "" {
			out.auth.Forges = append(out.auth.Forges, authService.ForgeApp{Host: f.Host, ClientID: f.ClientID})
		}
		secret(authService.OAuthSecretName(f.Host), f.ClientSecret)
	}
	for _, o := range a.OIDC {
		out.auth.OIDC = append(out.auth.OIDC, authService.OIDCProvider{Name: o.Name, Issuer: o.Issuer, ClientID: o.ClientID, Scopes: o.Scopes})
		secret(authService.OIDCSecretName(o.Name), o.ClientSecret)
	}

	out.trusted = m.Domains.Trusted
	if m.ACME.AccountKey != "" {
		key, err := p.Cloud().AccountKey(m.ACME)
		if err != nil {
			return nil, fmt.Errorf("cloud.cm.acme.accountkey: %w", err)
		}
		out.cm.Certificate.ACME = certificate.ACME{Enabled: true, Directory: m.ACME.Directory, Email: m.ACME.Email, EAB: m.ACME.EAB}
		out.secrets = append(out.secrets, cloudSecret{ref: secrets.Ref{Scope: secrets.Cloud(cm.VaultService), Name: cm.AccountSecret}, value: string(key)})
	}
	return out, nil
}
