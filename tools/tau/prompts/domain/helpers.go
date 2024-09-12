package domainPrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	domainFlags "github.com/taubyte/tau/tools/tau/flags/domain"
	domainLib "github.com/taubyte/tau/tools/tau/lib/domain"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func certificate(ctx *cli.Context, domain *structureSpec.Domain, new bool) (err error) {
	defaultCertType := domainFlags.CertTypeAuto
	if !new {
		defaultCertType = domain.CertType
	}

	domain.CertType, err = getCertType(ctx, defaultCertType)
	if err != nil {
		return
	}

	if domain.CertType == domainFlags.CertTypeInline {
		if new {
			domain.CertFile = GetOrRequireACertificate(ctx, CertificateFilePrompt)
			domain.KeyFile = GetOrRequireAKey(ctx, KeyFilePrompt)
		} else {
			domain.CertFile = GetOrRequireACertificate(ctx, CertificateFilePrompt, domain.CertFile)
			domain.KeyFile = GetOrRequireAKey(ctx, KeyFilePrompt, domain.KeyFile)
		}

		var (
			cert []byte
			key  []byte
		)
		cert, key, err = domainLib.ValidateCertificateKeyPairAndHostname(domain)
		if err != nil {
			// TODO verbose
			return
		}

		domain.CertFile = string(cert)
		domain.KeyFile = string(key)
	}

	return nil
}

func getCertType(ctx *cli.Context, defaultCertType string) (certType string, err error) {
	certType, isSet, err := domainFlags.GetCertType(ctx)
	if err != nil {
		return
	}

	if !isSet {
		certType, err = prompts.SelectInterface(domainFlags.CertTypeOptions, CertificateTypePrompt, defaultCertType)
		if err != nil {
			return
		}
	}

	return
}
