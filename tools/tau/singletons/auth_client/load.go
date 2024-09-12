//go:build !localAuthClient

package authClient

import (
	client "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/tools/tau/i18n"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
)

func Load() (*client.Client, error) {
	if _client == nil {
		profile, client, err := loadClient()
		if err != nil {
			return nil, singletonsI18n.LoadingAuthClientFailed(err)
		}

		_, err = client.User().Get()
		if err != nil {
			i18n.Help().TokenMayBeExpired(profile.Name())
			return nil, err
		}

		_client = client
	}

	return _client, nil
}
