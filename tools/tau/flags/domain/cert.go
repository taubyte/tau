package domainFlags

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slices"
)

const (
	CertTypeAuto   = "auto"
	CertTypeInline = "inline"
)

var (
	CertTypeOptions = []string{CertTypeInline, CertTypeAuto}

	Certificate = &cli.StringFlag{
		Name:    "certificate",
		Aliases: []string{"c"},
		Usage:   i18n.NotImplemented,
	}

	Key = &cli.StringFlag{
		Name:    "key",
		Aliases: []string{"k"},
		Usage:   i18n.NotImplemented,
	}

	CertType = &cli.StringFlag{
		Name:    "cert-type",
		Aliases: []string{"type"},
		Usage:   fmt.Sprintf("Type of certificate to use, currently inline is %s; %s", i18n.NotImplementedLC, flags.UsageOneOfOption(CertTypeOptions)),
	}
)

func GetCertType(c *cli.Context) (certType string, isSet bool, err error) {
	isSet = c.IsSet(CertType.Name)
	if c.IsSet(CertType.Name) {
		certType = c.String(CertType.Name)

		if !slices.Contains(CertTypeOptions, certType) {
			return "", false, fmt.Errorf("cert-type must be one of %s", strings.Join(CertTypeOptions, ", "))
		}
	}

	return
}
