package cli

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/forge"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/tools/tau/internal/api"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

// setForge records f in profile name, unsaved. A nil f changes nothing.
func setForge(cfg *config.Config, name string, f *authv2.ForgeToken) {
	if f == nil || f.GetHost() == "" || f.GetAccessToken() == "" {
		return
	}
	p := cfg.Profiles[name]
	if p.Forges == nil {
		p.Forges = map[string]config.ForgeToken{}
	}
	p.Forges[f.GetHost()] = config.ForgeToken{Token: f.GetAccessToken(), Refresh: f.GetRefreshToken(), Expires: f.GetExpiresUnix(), Login: f.GetLogin()}
	cfg.Profiles[name] = p
}

// forgeCreds hands out the profile's forge credentials, renewing one through
// the cloud's OAuth app when it is about to expire.
type forgeCreds struct {
	env  Env
	cfg  *config.Config
	name string
	cl   *api.Client
	now  func() time.Time
}

func (f forgeCreds) credential(ctx context.Context, host string) (forge.Credential, error) {
	tok, ok := f.cfg.Profiles[f.name].Forges[host]
	if !ok || tok.Token == "" {
		return forge.Credential{}, fmt.Errorf("profile %q holds no %s credential: sign in to it with `tau forge connect %s`", f.name, host, host)
	}
	if tok.Expires == 0 || f.now().Unix() < tok.Expires-renewForgeWithin {
		return forge.Token(tok.Token), nil
	}
	if tok.Refresh == "" {
		return forge.Credential{}, fmt.Errorf("your %s credential expired and cannot be renewed: sign in again with `tau forge connect %s`", host, host)
	}
	renewed, err := f.cl.Sessions().Renew(ctx, connect.NewRequest(&authv2.RenewRequest{Host: host, RefreshToken: tok.Refresh}))
	if err != nil {
		return forge.Credential{}, fmt.Errorf("renewing your %s credential: %w (or sign in again with `tau forge connect %s`)", host, err, host)
	}
	r := renewed.Msg
	if r.GetHost() == "" {
		r.Host = host
	}
	if r.GetRefreshToken() == "" {
		r.RefreshToken = tok.Refresh
	}
	setForge(f.cfg, f.name, r)
	if err := f.cfg.Save(); err != nil {
		fmt.Fprintf(f.env.Stderr, "warning: saving the renewed %s credential: %v\n", host, err)
	}
	return forge.Token(r.GetAccessToken()), nil
}

// renewForgeWithin is how close to its expiry a forge credential is renewed
// before use, in seconds.
const renewForgeWithin = 60
