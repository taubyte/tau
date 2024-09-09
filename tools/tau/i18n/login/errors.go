package loginI18n

import (
	"errors"
	"fmt"
)

const (
	createFailed          = "creating login `%s` failed with: %s"
	selectFailed          = "selecting login `%s` failed with: %s"
	selectProviderFailed  = "selecting provider failed with: %s"
	selectTokenFromFailed = "selecting token from where failed with: %s"
	getProfilesFailed     = "getting profiles failed with: %s"
	doesNotExistIn        = "login `%s` does not exist in: %v"
	tokenPromptFailed     = "getting token failed with: %s"

	removingDefaultFailed = "removing default profile failed with: %s"
	settingDefaultFailed  = "setting default profile failed with: %s"

	listingEmailsFailed   = "listing emails failed with: %s"
	gettingUserInfoFailed = "getting git user info failed with: %s"
	gitNameOrEmailFailed  = "getting git name or email failed with: %s.  Edit it in your ~/tau.yaml file"
)

var (
	ErrorNoEmailsFound = errors.New("no emails found")
)

func CreateFailed(login string, err error) error {
	return fmt.Errorf(createFailed, login, err)
}

func SelectFailed(login string, err error) error {
	return fmt.Errorf(selectFailed, login, err)
}

func SelectProviderFailed(err error) error {
	return fmt.Errorf(selectProviderFailed, err)
}

func SelectTokenFromFailed(err error) error {
	return fmt.Errorf(selectTokenFromFailed, err)
}

func GetProfilesFailed(err error) error {
	return fmt.Errorf(getProfilesFailed, err)
}

func DoesNotExistIn(login string, locs []string) error {
	return fmt.Errorf(doesNotExistIn, login, locs)
}

func TokenPromptFailed(err error) error {
	return fmt.Errorf(tokenPromptFailed, err)
}

func RemovingDefaultFailed(err error) error {
	return fmt.Errorf(removingDefaultFailed, err)
}

func SettingDefaultFailed(err error) error {
	return fmt.Errorf(settingDefaultFailed, err)
}

func ListingEmailsFailed(err error) error {
	return fmt.Errorf(listingEmailsFailed, err)
}

func GettingUserInfoFailed(err error) error {
	return fmt.Errorf(gettingUserInfoFailed, err)
}

func GitNameOrEmailFailed(err error) error {
	return fmt.Errorf(gitNameOrEmailFailed, err)
}
