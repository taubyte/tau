package domainPrompts

import (
	domainFlags "github.com/taubyte/tau/tools/tau/flags/domain"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func GetGeneratedFQDN(ctx *cli.Context, prev ...bool) bool {
	return prompts.GetOrAskForBool(ctx, domainFlags.Generated.Name, "Generate an FQDN:")
}

func GetGeneratedFQDNPrefix(ctx *cli.Context, prev ...string) string {
	if !prompts.PromptEnabled {
		return ctx.String(domainFlags.GeneratedPrefix.Name)
	}

	return prompts.GetOrAskForAStringValue(ctx, domainFlags.GeneratedPrefix.Name, "Generated FQDN prefix (empty for none):")
}

func GetOrRequireAnFQDN(c *cli.Context, prev ...string) string {
	return prompts.GetOrRequireAString(c, domainFlags.FQDN.Name, FQDNPrompt, validate.FQDNValidator, prev...)
}

// TODO get cert and key + use ValidateCertificateKeyPairAndHostname
// Possibly get from file, currently disabled functionality due to no way to store cert and key files

func GetOrRequireACertificate(c *cli.Context, prev ...string) string {
	return prompts.GetOrRequireAString(c, domainFlags.Certificate.Name, FQDNPrompt, nil, prev...)
}

func GetOrRequireAKey(c *cli.Context, prev ...string) string {
	return prompts.GetOrRequireAString(c, domainFlags.Key.Name, FQDNPrompt, nil, prev...)
}
