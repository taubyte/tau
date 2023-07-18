package client

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

var (
	mockAuthUrl     = "http://localhost:8083"
	mockGitProvider = "github"
	mockGitHubToken = "123456"
)

func newTestClientWithWrongGitProviderToMockServer(provider string) (*Client, error) {
	ctx := context.Background()
	client, err := New(ctx, URL(mockAuthUrl), Auth(mockGitHubToken), Provider(provider))
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newTestClientWithWrongURLToMockServer(url string) (*Client, error) {
	ctx := context.Background()
	client, err := New(ctx, URL(url), Auth(mockGitHubToken), Provider(mockGitProvider))
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newTestClientWithWrongTokenToMockServer(token string) (*Client, error) {
	ctx := context.Background()
	client, err := New(ctx, URL(mockAuthUrl), Auth(token), Provider(mockGitProvider))
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newTestClientToMockServer() (*Client, error) {
	ctx := context.Background()
	client, err := New(ctx, URL(mockAuthUrl), Auth(mockGitHubToken), Provider(mockGitProvider))
	if err != nil {
		return nil, err
	}
	return client, nil
}

func clearMockServer(t *testing.T, client *Client) {
	client.get("/clear", t)
}

func StartMockServer() (deferment func(), err error) {
	mockServerContext, mockServerStop := context.WithCancel(context.Background())

	cmd := exec.CommandContext(mockServerContext, "python3", "test_server")

	deferment = func() {
		mockServerStop()

		// Give mock server time to stop
		time.Sleep(1 * time.Second)
	}

	err = cmd.Start()
	if err != nil {
		err = fmt.Errorf("starting mock server failed with: %s", err)
		return
	}

	// Give mock server time to start
	time.Sleep(500 * time.Millisecond)

	return
}

func MockServerAndClient(t *testing.T) (client *Client, deferment func(), err error) {
	deferment, err = StartMockServer()
	if err != nil {
		return
	}

	client, err = newTestClientToMockServer()
	return
}
