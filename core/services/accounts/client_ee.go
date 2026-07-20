//go:build ee

package accounts

import accountsEE "github.com/taubyte/tau/ee/core/services/accounts"

// eeSurface is defined in the ee package and only aliased here.
type eeSurface = accountsEE.ClientSurface
