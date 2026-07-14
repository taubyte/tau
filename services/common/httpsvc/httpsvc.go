package httpsvc

import (
	"context"
	"strings"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/config"
	service "github.com/taubyte/tau/pkg/http"
	auto "github.com/taubyte/tau/pkg/http-auto"
	autoOpts "github.com/taubyte/tau/pkg/http-auto/options"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	basicHttpSecure "github.com/taubyte/tau/pkg/http/basic/secure"
	"github.com/taubyte/tau/pkg/http/options"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

// New wires the standard tau HTTP listener: autocert HTTPS in production,
// plain / self-signed-secure in dev. Replaces the deleted cfg-aware
// auto.New shim — pkg/http-auto itself is now pure options.
func New(ctx context.Context, node peer.Node, cfg config.Config, extra ...options.Option) (service.Service, error) {
	ops := append(AutoOptsFromConfig(cfg), extra...)
	if cfg.DevMode() {
		return devHTTP(ctx, cfg.EnableHTTPS(), ops...)
	}
	return auto.New(ctx, node, ops...)
}

func devHTTP(ctx context.Context, enableHTTPS bool, ops ...options.Option) (service.Service, error) {
	if !enableHTTPS {
		return basicHttp.New(ctx, ops...)
	}
	ops = append(ops, options.SelfSignedCertificate())
	return basicHttpSecure.New(ctx, ops...)
}

// AutoOptsFromConfig translates a tau config into the option set
// pkg/http-auto expects: listen addr, client-node, the two domain predicates
// (auto-trust for service+alias domains, skip-proof for generated subdomains),
// custom ACME directory + CA trust knobs, and debug.
func AutoOptsFromConfig(cfg config.Config) []options.Option {
	ops := []options.Option{
		options.Listen(cfg.HttpListen()),
		autoOpts.ClientNode(cfg.ClientNode()),
		autoOpts.AutoTrustDomain(autoTrustFromConfig(cfg)),
		autoOpts.SkipDomainProof(cfg.GeneratedDomainMatch),
	}
	if cfg.CustomAcme() {
		ops = append(ops, options.ACMEWithKey(cfg.AcmeUrl(), cfg.AcmeKey()))
	}
	if cfg.AcmeCAInsecureSkipVerify() {
		ops = append(ops, options.ACMECASkipVerify(true))
	}
	if roots := cfg.AcmeRootCA(); roots != nil {
		ops = append(ops, options.ACMECARoots(roots))
	}
	if cfg.Verbose() {
		ops = append(ops, options.Debug())
	}
	return ops
}

// autoTrustFromConfig folds the three "trust this host without TNS proof"
// cfg predicates into one closure: an exact-match against `<svc>.<NetworkFqdn>`
// for each known tau service, plus alias and services-domain regex matches.
func autoTrustFromConfig(cfg config.Config) func(string) bool {
	return func(host string) bool {
		host = strings.TrimSuffix(host, ".")
		fqdn := cfg.NetworkFqdn()
		for _, srv := range commonSpecs.Services {
			if host == srv+"."+fqdn {
				return true
			}
		}
		if cfg.AliasDomainsMatch(host) {
			return true
		}
		// custom domains bound to a service (domains.hosts) are ours to serve.
		if _, ok := cfg.ServiceForHost(host); ok {
			return true
		}
		return cfg.ServicesDomainMatch(host)
	}
}
