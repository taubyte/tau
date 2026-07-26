//go:build !ee

package accounts

import accountsIface "github.com/taubyte/tau/core/services/accounts"

// eeSurface adds no methods in the community build.
type eeSurface interface{}

func newInProcessClient(srv *AccountsService) accountsIface.Client {
	return newBase(srv)
}
