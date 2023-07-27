package git

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/clients/http/auth/git/common"
	githubClient "github.com/taubyte/tau/clients/http/auth/git/github"
)

func New(ctx context.Context, provider, token string) common.Client {
	switch provider {
	case "github":
		return githubClient.New(ctx, token)
	default:
		panic(fmt.Sprintf("provider `%s` is not supported", provider))
	}
}
