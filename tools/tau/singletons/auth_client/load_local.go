//go:build localAuthClient

package authClient

import (
	client "github.com/taubyte/tau/clients/http/auth"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
)

func Load() (*client.Client, error) {
	if _client == nil {
		_, client, err := loadClient()
		if err != nil {
			return nil, singletonsI18n.LoadingAuthClientFailed(err)
		}

		_client = client
	}

	return _client, nil
}
