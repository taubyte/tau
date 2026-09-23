package basic_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/taubyte/tau/pkg/http/options"

	basic "github.com/taubyte/tau/pkg/http/basic"
	"gotest.tools/v3/assert"
)

// A service asked for port 0 reports the port it was given. Nothing can learn a
// kernel-assigned port unless the bind happens where the caller can see it, and
// a caller that cannot learn it has to predict one instead — which is a port
// another process may take between the prediction and the bind.
func TestAServiceAskedForPortZeroReportsThePortItGot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := basic.New(ctx, options.Listen("127.0.0.1:0"))
	assert.NilError(t, err)

	srv.Start()
	assert.NilError(t, srv.Error())

	listen, err := srv.GetListenAddress()
	assert.NilError(t, err)
	assert.Assert(t, listen.Port() != "0", "still reporting the port it asked with")

	// AND IT IS THE PORT IT SERVES ON, not merely a number it remembered.
	resp, err := http.Get(listen.String())
	assert.NilError(t, err)
	resp.Body.Close()
}

// A port that cannot be taken is an error the caller sees, not a silence.
func TestAPortThatCannotBeTakenIsReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	held, err := basic.New(ctx, options.Listen("127.0.0.1:0"))
	assert.NilError(t, err)
	held.Start()
	assert.NilError(t, held.Error())

	taken, err := held.GetListenAddress()
	assert.NilError(t, err)

	srv, err := basic.New(ctx, options.Listen(taken.Host))
	assert.NilError(t, err)
	srv.Start()
	assert.Assert(t, srv.Error() != nil, "bound a port another listener holds")
}
