package loginLib

import (
	loginI18n "github.com/taubyte/tau/tools/tau/i18n/login"
	"github.com/taubyte/tau/tools/tau/states"
)

func extractInfo(token, provider string) (name, email string, err error) {
	// TODO provider

	client := githubApiClient(token)

	user, _, err := client.Users.Get(
		states.Context,
		"",
	)
	if err != nil {
		err = loginI18n.GettingUserInfoFailed(err)
		return
	}

	name = user.GetLogin()

	emails, _, err := client.Users.ListEmails(
		states.Context,
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
