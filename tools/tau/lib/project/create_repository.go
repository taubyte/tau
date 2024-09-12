//go:build !localAuthClient

package projectLib

import (
	client "github.com/taubyte/tau/clients/http/auth"
)

func CreateRepository(client *client.Client, name, description string, private bool) (id string, err error) {
	return client.CreateRepository(name, description, private)
}
