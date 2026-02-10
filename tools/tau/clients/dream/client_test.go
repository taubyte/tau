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
	assert.NilError(t, session.Set().DreamAPIURL("http://127.0.0.1:12345"))

	client, err := Client(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, client != nil)
}

func TestStatus_ReturnsErrorWhenUnreachable(t *testing.T) {
	session.Clear()
	t.Cleanup(session.Clear)

	assert.NilError(t, session.LoadSessionInDir(t.TempDir()))
	// Unreachable URL so Chart() will fail (connection refused)
	assert.NilError(t, session.Set().DreamAPIURL("http://127.0.0.1:19999"))

	_, err := Status(context.Background())
	assert.Assert(t, err != nil)
}

func TestHTTPPort_WhenStatusFails(t *testing.T) {
	session.Clear()
	t.Cleanup(session.Clear)

	assert.NilError(t, session.LoadSessionInDir(t.TempDir()))
	assert.NilError(t, session.Set().DreamAPIURL("http://127.0.0.1:19999"))

	port, err := HTTPPort(context.Background(), "patrick")
	assert.Assert(t, err != nil)
	assert.Equal(t, port, 0)
}
