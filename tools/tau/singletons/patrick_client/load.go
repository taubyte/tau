package patrickClient

import (
	patrickClient "github.com/taubyte/tau/clients/http/patrick"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
)

func Load() (*patrickClient.Client, error) {
	if _client == nil {
		_, client, err := loadClient()
		if err != nil {
			return nil, singletonsI18n.LoadingPatrickClientFailed(err)
		}

		_client = client
	}

	return _client, nil
}
