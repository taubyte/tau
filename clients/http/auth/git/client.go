package git

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/clients/http/auth/git/common"
	githubClient "github.com/taubyte/tau/clients/http/auth/git/github"
)

func New(ctx context.Context, provider, token string) (common.Client, error) {
	switch provider {
	case "github":
		return githubClient.New(ctx, token), nil
	default:
		return nil, fmt.Errorf("provider `%s` is not supported", provider)
	}
}
