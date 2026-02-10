package domainFlags_test

import (
	"testing"

	domainFlags "github.com/taubyte/tau/tools/tau/flags/domain"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestFQDNFlag(t *testing.T) {
	app := &cli.App{
		Flags: []cli.Flag{domainFlags.FQDN},
		Action: func(ctx *cli.Context) error {
			assert.Equal(t, ctx.String("fqdn"), "example.com")
			return nil
		},
	}
	err := app.Run([]string{"app", "--fqdn", "example.com"})
	assert.NilError(t, err)
}
