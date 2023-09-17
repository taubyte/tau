package patrick // Consider using a more descriptive name that represents the domain or service being interacted with.

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/clients/http"
)

func New(ctx context.Context, options ...http.Option) (*Client, error) {
	c, err := http.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("new patrick client failed with: %w", err)
	}

	return &Client{c}, nil 
}
