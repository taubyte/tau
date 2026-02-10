package loginLib

import (
	"context"

	loginI18n "github.com/taubyte/tau/tools/tau/i18n/login"
)

// ExtractInfoStub, when non-nil, is used instead of calling the real GitHub API.
// Set in tests to avoid network calls. Signature: (token, provider) -> (gitName, gitEmail, err).
var ExtractInfoStub func(token, provider string) (name, email string, err error)

func extractInfo(token, provider string) (name, email string, err error) {
	if ExtractInfoStub != nil {
		return ExtractInfoStub(token, provider)
	}
	return extractInfoReal(token, provider)
}

func extractInfoReal(token, provider string) (name, email string, err error) {
	// TODO provider

	client := githubApiClient(token)

	user, _, err := client.Users.Get(
		context.Background(),
		"",
	)
	if err != nil {
		err = loginI18n.GettingUserInfoFailed(err)
		return
	}

	name = user.GetLogin()

	emails, _, err := client.Users.ListEmails(
		context.Background(),
		nil,
	)
	if err != nil {
		err = loginI18n.ListingEmailsFailed(err)
		return
	}
	if len(emails) == 0 {
		err = loginI18n.ErrorNoEmailsFound
		return
	}

	email = emails[0].GetEmail()

	return
}
