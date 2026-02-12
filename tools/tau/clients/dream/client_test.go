package dream

import (
	"context"
	"testing"

	"github.com/taubyte/tau/tools/tau/session"
	"gotest.tools/v3/assert"
)

func TestClient_NoSessionURL(t *testing.T) {
	session.Clear()
	client, err := Client(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, client != nil)
}

func TestClient_WithSessionURL(t *testing.T) {
	session.Clear()
	t.Cleanup(session.Clear)

	assert.NilError(t, session.LoadSessionInDir(t.TempDir()))

	client, err := Client(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, client != nil)
}

func TestStatus_ReturnsErrorWhenUnreachable(t *testing.T) {
	session.Clear()
	t.Cleanup(session.Clear)

	assert.NilError(t, session.LoadSessionInDir(t.TempDir()))
	// No dream on default port; Status() will fail (connection refused or no universe)
	_, err := Status(context.Background())
	assert.Assert(t, err != nil)
}

func TestHTTPPort_WhenStatusFails(t *testing.T) {
	session.Clear()
	t.Cleanup(session.Clear)

	assert.NilError(t, session.LoadSessionInDir(t.TempDir()))

	port, err := HTTPPort(context.Background(), "patrick")
	assert.Assert(t, err != nil)
	assert.Equal(t, port, 0)
}
