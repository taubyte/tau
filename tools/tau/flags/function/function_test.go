package functionFlags

import (
	"testing"

	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestTypeFlag(t *testing.T) {
	assert.Assert(t, Type != nil)
	assert.Equal(t, Type.Name, "type")
}

func TestTypeFlagWithValue(t *testing.T) {
	app := &cli.App{
		Flags: []cli.Flag{Type},
		Action: func(ctx *cli.Context) error {
			assert.Equal(t, ctx.String("type"), "http")
			return nil
		},
	}
	err := app.Run([]string{"app", "--type", "http"})
	assert.NilError(t, err)
}

func TestCategoryVars(t *testing.T) {
	assert.Equal(t, CategoryHttp, "HTTP(S)")
	assert.Equal(t, CategoryP2P, "P2P")
	assert.Equal(t, CategoryPubSub, "PUBSUB")
}

func TestHttpReturnsFlags(t *testing.T) {
	flags := Http()
	assert.Assert(t, len(flags) >= 1)
}

func TestP2PReturnsFlags(t *testing.T) {
	flags := P2P()
	assert.Assert(t, len(flags) >= 1)
}

func TestPubSubReturnsFlags(t *testing.T) {
	flags := PubSub()
	assert.Assert(t, len(flags) >= 1)
}
