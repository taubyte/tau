package client

import (
	"context"
	"os"
	"testing"

	"github.com/taubyte/tau/clients/http"
	"gotest.tools/v3/assert"
)

// TODO: use dreamland instead of a deployed auth
var (
	authNodeUrl = "https://auth.tau.sandbox.taubyte.com"
	testToken   = os.Getenv("TEST_GIT_TOKEN")
)

func newTestClient(t *testing.T) (*Client, error) {
	if testToken == "" {
		t.SkipNow()
	}
	ctx := context.Background()
	client, err := New(ctx, http.URL(authNodeUrl), http.Auth(testToken), http.Provider(http.Github))
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newTestUnsecureClient(t *testing.T) (*Client, error) {
	if testToken == "" {
		t.SkipNow()
	}
	ctx := context.Background()
	client, err := New(ctx, http.URL(authNodeUrl), http.Auth(testToken), http.Provider(http.Github), http.Unsecure())
	if err != nil {
		return nil, err
	}
	return client, nil
}

func TestConnectionToProdNodeWithoutCheckingCertificates(t *testing.T) {
	t.Run("Given an Unsecure Client with a valid token", func(t *testing.T) {
		client, err := newTestUnsecureClient(t)
		assert.NilError(t, err)

		t.Run("Getting /me", func(t *testing.T) {
			me := client.User()
			_, err := me.Get()
			assert.NilError(t, err)
		})
	})
}

func TestConnectionToProdNode(t *testing.T) {
	t.Run("Given a Client with a valid token", func(t *testing.T) {
		client, err := newTestClient(t)
		assert.NilError(t, err)

		t.Run("Getting /me", func(t *testing.T) {
			me := client.User()
			_, err := me.Get()
			assert.NilError(t, err)
		})
	})
}
