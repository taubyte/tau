package client

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/clients/http"

	git "github.com/taubyte/tau/clients/http/auth/git"
)

// New returns a new Client based on the options provided and an error
func New(ctx context.Context, options ...http.Option) (*Client, error) {
	client, err := http.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("new auth client failed with: %w", err)
	}

	return &Client{
		Client:    client,
		gitClient: git.New(ctx, client.Provider(), client.Token()),
	}, nil
}
