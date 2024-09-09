package loginLib

import (
	"github.com/google/go-github/github"
	"github.com/taubyte/tau/tools/tau/states"
	"golang.org/x/oauth2"
)

func githubApiClient(token string) *github.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(states.Context, ts)

	return github.NewClient(tc)
}
