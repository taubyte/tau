//go:build dreaming

package tests

import (
	"strings"
	"testing"

	golog "github.com/ipfs/go-log/v2"
	commonIface "github.com/taubyte/tau/core/common"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/dream"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
)

// TestCipherNone_Dreaming proves the invariant that matters most about the
// at-rest cipher seam: a hoarder with no cipher configured must still boot and
// serve normally, loudly warning rather than refusing to start. It stands up a
// universe with hoarder + seer, confirms the service came up (no error out of
// hoarder.New via StartWithConfig), round-trips a kv put/get, and asserts the
// unencrypted-at-rest warning fired.
func TestCipherNone_Dreaming(t *testing.T) {
	fastConvergence(t)

	core, logs := observer.New(zapcore.WarnLevel)
	prev := golog.GetConfig()
	golog.SetPrimaryCore(core)
	if err := golog.SetLogLevel("tau.hoarder.service", "warn"); err != nil {
		t.Fatalf("setting log level failed: %v", err)
	}
	t.Cleanup(func() { golog.SetupLogging(prev) })

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	// No cipher is configured anywhere here; StartWithConfig must still succeed.
	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":    {},
			"hoarder": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Seer:    &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)
	hoarderClient, err := simple.Hoarder()
	assert.NilError(t, err)

	kv, err := hoarderClient.KVDB(hoarderIface.Global, "nocipherproj", "", "/nocipher/instance", "main")
	assert.NilError(t, err)

	ctx := u.Context()
	assert.NilError(t, kv.Put(ctx, "k1", []byte("v1")))

	got, err := kv.Get(ctx, "k1")
	assert.NilError(t, err)
	assert.Equal(t, string(got), "v1")

	var found bool
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "hoarder values unencrypted at rest") {
			found = true
			break
		}
	}
	assert.Assert(t, found, "expected the unencrypted-at-rest warning to fire")
}
