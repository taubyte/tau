package client

import (
	"context"
	"net/http"
	"os"
	"testing"

	"gotest.tools/v3/assert"
)

var (
	authNodeUrl  = "https://auth.taubyte.com"
	testProvider = "github"
	testToken    = testGitToken()
)

func testGitToken() string {
	token := os.Getenv("TEST_GIT_TOKEN")

	if token == "" {
		panic("TEST_GIT_TOKEN not set")
	}

	return token
}

func newTestClient() (*Client, error) {
	ctx := context.Background()
	client, err := New(ctx, URL(authNodeUrl), Auth(testToken), Provider(testProvider))
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newTestUnsecureClient() (*Client, error) {
	ctx := context.Background()
	client, err := New(ctx, URL(authNodeUrl), Auth(testToken), Provider(testProvider), Unsecure())
	if err != nil {
		return nil, err
	}
	return client, nil
}

func TestConnectionToProdNodeWithoutCheckingCertificates(t *testing.T) {
	t.Run("Given an Unsecure Client with a valid token", func(t *testing.T) {
		client, err := newTestUnsecureClient()
		assert.NilError(t, err)

		if client.unsecure != true {
			t.Error("Failed to set Unsecure Option")
			return
		}

		if transport, ok := client.client.Transport.(*http.Transport); ok == true && transport.TLSClientConfig.InsecureSkipVerify != true {
			t.Error("Failed to set InsecureSkipVerify in TLS config")
			return
		}

		t.Run("Getting /me", func(t *testing.T) {
			me := client.User()
			_, err := me.Get()
			assert.NilError(t, err)
		})
	})
}

func TestConnectionToProdNode(t *testing.T) {
	t.Run("Given a Client with a valid token", func(t *testing.T) {
		client, err := newTestClient()
		assert.NilError(t, err)

		if client.unsecure != false {
			t.Error("Something is forcing unsecure to `true`")
			return
		}

		if transport, ok := client.client.Transport.(*http.Transport); ok == true && transport.TLSClientConfig.InsecureSkipVerify == true {
			t.Error("InsecureSkipVerify in TLS config is set to `true`!")
			return
		}

		if transport, ok := client.client.Transport.(*http.Transport); ok == true && transport.TLSClientConfig.RootCAs != rootCAs {
			t.Error("Not using RootCAs!")
			return
		}

		t.Run("Getting /me", func(t *testing.T) {
			me := client.User()
			_, err := me.Get()
			assert.NilError(t, err)
		})
	})
}
