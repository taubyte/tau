package drive

import (
	"fmt"
	"slices"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/node/boot"
	"github.com/taubyte/tau/pkg/certificate"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/tools/spore-drive/config"
)

// cloudServices is what every node is told about sign-in and certificates,
// and seed what of them is secret: the vault's, written by one node.
type cloudServices struct {
	auth   authService.Config
	cm     cm.Config
	forges []node.Forge
	seed   []boot.Secret
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
	if a.Admin.Name != "" {
		hash, err := authService.HashAdminPassword(a.Admin.Password)
		if err != nil {
			return nil, fmt.Errorf("cloud.auth.admin: %w", err)
		}
		out.auth.Admin = authService.Admin{Name: a.Admin.Name, PasswordHash: hash}
	}
	secret := func(name, value string) {
		if value != "" {
			out.seed = append(out.seed, boot.Secret{Project: authService.VaultProject, Environment: authService.VaultEnvironment, Name: name, Value: value})
		}
	}
	for _, f := range a.Forges {
		if f.Driver != "" {
			out.forges = append(out.forges, node.Forge{Host: f.Host, Driver: f.Driver})
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

	out.cm.Domains.Trusted = m.Domains.Trusted
	if m.ACME.AccountKey != "" {
		key, err := p.Cloud().AccountKey(m.ACME)
		if err != nil {
			return nil, fmt.Errorf("cloud.cm.acme.accountkey: %w", err)
		}
		out.cm.Certificate.ACME = certificate.ACME{Enabled: true, Directory: m.ACME.Directory, Email: m.ACME.Email, EAB: m.ACME.EAB}
		out.seed = append(out.seed, boot.Secret{Project: cm.VaultProject, Environment: cm.VaultEnvironment, Name: cm.AccountSecret, Value: string(key)})
	}
	return out, nil
}

// seedsFrom is whether hostName's node writes the seed: the first host by name.
func seedsFrom(p config.Parser, hostName string) bool {
	return hostName == slices.Min(p.Hosts().List())
}
