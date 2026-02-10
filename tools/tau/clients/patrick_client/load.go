package patrickClient

import (
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
)

func Load() (Client, error) {
	_, client, err := loadClient()
	if err != nil {
		return nil, singletonsI18n.LoadingPatrickClientFailed(err)
	}

	return client, nil
}
