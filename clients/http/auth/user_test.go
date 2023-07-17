package client

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestUser(t *testing.T) {
	deferment, err := StartMockServer()
	assert.NilError(t, err)
	defer deferment()

	client, err := newTestClientToMockServer()
	assert.NilError(t, err)

	user, err := client.User().Get()
	assert.NilError(t, err)

	assert.Equal(t, user.Login, "test_user")

	clearMockServer(t, client)

	// Test bad token
	client, err = newTestClientWithWrongTokenToMockServer("41902")
	assert.NilError(t, err)

	_, err = client.User().Get()
	assert.ErrorContains(t, err, "401 UNAUTHORIZED")

	clearMockServer(t, client)

	// Test bad URL
	client, err = newTestClientWithWrongURLToMockServer("http://127.0.0.1:8084")
	assert.NilError(t, err)

	_, err = client.User().Get()
	assert.ErrorContains(t, err, "connect: connection refused")

	clearMockServer(t, client)

	// Test bad Provider
	client, err = newTestClientWithWrongGitProviderToMockServer("gitlab")
	assert.ErrorContains(t, err, "new client provider option `gitlab` unknown")
}
