package ingress

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAReplacedHookSecretVerifiesForTheGraceOnly(t *testing.T) {
	h := newHarness(t, Config{HookSecretGrace: 10 * time.Minute, SecretCacheTTL: time.Hour})
	replaced := h.clock.now()
	h.hooks.get = func(id string) (Hook, error) {
		return Hook{Project: id, Repositories: []HookRepository{{
			Key: testRepoKey, Secret: []byte("new"), Previous: []byte("old"), Replaced: replaced,
		}}}, nil
	}

	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{signature: "old"})).Code)
	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{signature: "new"})).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, webhookRequest(webhookOpts{signature: "older"})).Code,
		"a secret the previous one replaced is not accepted")

	h.clock.advance(9 * time.Minute)
	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{signature: "old"})).Code)

	h.clock.advance(time.Minute)
	require.Equal(t, http.StatusNotFound, h.do(t, webhookRequest(webhookOpts{signature: "old"})).Code)
	require.Equal(t, http.StatusAccepted, h.do(t, webhookRequest(webhookOpts{signature: "new"})).Code)
	require.Len(t, h.backend.enqueues(), 4)
}

func TestTheHookSecretGraceDefaultsAndIsNeverNegative(t *testing.T) {
	c, err := Config{}.withDefaults()
	require.NoError(t, err)
	require.Equal(t, DefaultHookSecretGrace, c.HookSecretGrace)

	_, err = Config{HookSecretGrace: -time.Second}.withDefaults()
	require.ErrorContains(t, err, "hookSecretGrace")
}
