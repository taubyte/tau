//go:build ee

package auth

import accountstest "github.com/taubyte/tau/ee/core/services/accounts/accountstest"

// eeStub fills in the ee-only accounts Client methods under -tags ee. The set is
// defined once in the ee tree; this file only imports and embeds it.
type eeStub = accountstest.Stub
