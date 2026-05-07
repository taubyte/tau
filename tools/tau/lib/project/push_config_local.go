//go:build localAuthClient

package projectLib

import (
	httpClient "github.com/taubyte/tau/clients/http/auth"
)

func cloneProjectAndPushConfig(clientProject *httpClient.Project, location, description, user string, embedToken bool, account, plan string) error {
	return nil
}
