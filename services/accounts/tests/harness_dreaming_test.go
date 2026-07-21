//go:build dreaming

package tests

import (
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"
)

// startAccountsUniverse boots a dream universe with only the accounts
// service running, so each test gets fresh KV state without any of the other
// services' setup cost. Shared by the community and ee dreaming tests.
func startAccountsUniverse(t *testing.T) *dream.Universe {
	t.Helper()

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"accounts": {},
		},
	}))

	// Service settle window matches the existing TestAccounts_Dreaming
	// pattern. Without this the first wire call sometimes lands before
	// the stream registers.
	time.Sleep(500 * time.Millisecond)
	return u
}
