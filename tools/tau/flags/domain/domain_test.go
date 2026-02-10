package domainFlags

import (
	"testing"

	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestFQDNFlag(t *testing.T) {
	assert.Assert(t, FQDN != nil)
	assert.Equal(t, FQDN.Name, "fqdn")
}

func TestGetCertType(t *testing.T) {
	app := &cli.App{
		Flags:  []cli.Flag{CertType},
		Action: func(ctx *cli.Context) error { return nil },
	}
	err := app.Run([]string{"app"})
	assert.NilError(t, err)

	app = &cli.App{
		Flags: []cli.Flag{CertType},
		Action: func(ctx *cli.Context) error {
			ct, isSet, err := GetCertType(ctx)
			assert.NilError(t, err)
			assert.Equal(t, isSet, true)
			assert.Equal(t, ct, CertTypeInline)
			return nil
		},
	}
	err = app.Run([]string{"app", "--cert-type", "inline"})
	assert.NilError(t, err)
}

func TestGetCertType_Invalid(t *testing.T) {
	app := &cli.App{
		Flags: []cli.Flag{CertType},
		Action: func(ctx *cli.Context) error {
			_, _, err := GetCertType(ctx)
			assert.ErrorContains(t, err, "must be one of")
			return nil
		},
	}
	err := app.Run([]string{"app", "--cert-type", "invalid"})
	assert.NilError(t, err)
}

func TestGeneratedFlags(t *testing.T) {
	assert.Assert(t, Generated != nil)
	assert.Assert(t, GeneratedPrefix != nil)
}
