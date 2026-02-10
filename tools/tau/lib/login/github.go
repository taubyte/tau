package loginLib

import (
	"context"

	"github.com/google/go-github/v71/github"
	"golang.org/x/oauth2"
)

func githubApiClient(token string) *github.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(context.Background(), ts)

	return github.NewClient(tc)
}
