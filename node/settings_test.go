package node

import (
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// settings_test ties this package's setting keys to the vocabulary.
//
// # The gap this closes
//
// Every backing here reads its target's fields out of a `Settings` map keyed by
// the vocabulary's compiled paths — and spells those paths itself, as private
// constants, in twelve files. services/worker spells the same paths, separately,
// when it fills the map. Until this, the two agreed with each other by copying
// and with the schema by luck: `keys/ttl` and `keys/size` were filed under a
// section name rather than an attribute path, and a database's credential under
// a key `database` does not declare.
//
// WHAT ROT LOOKS LIKE IS THE REASON THIS IS A TEST AND NOT A COMMENT. A renamed
// attribute does not fail: the key is absent, the read returns the zero value,
// and the backing falls back to its own default. `node/queue.go`'s own constant
// block is a post-mortem of exactly that — "every queue would open with
// pkg/queue's defaults and the author's visibility timeout, retry budget and
// dead letter would silently stop taking effect".
//
// ITS TESTS COULD NOT CATCH IT EITHER, which is why this one is shaped
// differently: every other test in this package builds its grants from these
// same constants, so it proves only that node agrees with itself.
func TestEverySettingKeyNamesAnAttribute(t *testing.T) {
	for kind, keys := range map[string][]string{
		"cache":   {cacheTTL, cacheSize, settingInstanceAdmission, settingInstanceLimit, settingInstanceValidator, settingInstanceScope},
		"volume":  {settingVolumeQuota, settingCheckpointFreq},
		"storage": {settingObjectType, settingObjectSize},
		"sync": {
			settingMaxAttempts, settingVisibility, settingDelay, settingDeadLetter,
			settingRetryEvery, settingRetryFactor, settingRetryCeiling,
			settingLogConsensus, settingLogRetentionAge, settingLogRetentionSize,
		},
		"channel":  {settingProtocol, settingServeKind, settingServeName, settingServeScope},
		"function": {settingAuthorizer, settingAuthorizerScope},
		"database": {databaseTypeSetting, "sql/" + databaseCredentialsSetting},
	} {
		for _, key := range keys {
			require.True(t, schema.IsAttribute(kind, key),
				"%s declares no attribute %q — the vocabulary renamed it and this backing still reads the old key, so it silently falls back to a default", kind, key)
		}
	}
}

// TestTHEQUEUESOWNPROMISEISREADORITISNOTMADE.
//
// A queue's `consensus` is Required by the vocabulary and takes `crdt` or
// `raft`. node/log.go refuses a `crdt` log by name — "this cloud serves raft
// logs and that one asks for crdt" — and nothing anywhere refuses a `crdt`
// QUEUE. An author who wrote the word got a raft queue and the opposite promise
// from the one they asked for, silently.
func TestAQueueDeclaresAConsensusSomethingHasToHonour(t *testing.T) {
	require.True(t, schema.IsAttribute("sync", settingQueueConsensus),
		"the vocabulary stopped declaring a queue's consensus, so the refusal below it is dead")

	q := queueing(t)

	// THE PROMISE IS KEPT OR THE QUEUE IS REFUSED BY NAME. Serving it with raft
	// machinery would answer the author's question with the other answer.
	err := q.Send(t.Context(), sendGrant("shop/prod/QmCrdt", map[string]string{
		settingQueueConsensus: "crdt",
	}), "1", []byte("x"))
	require.ErrorIs(t, err, ErrQueueConsensusUnserved)

	// AND A RAFT ONE IS SERVED, so the refusal is about the value and not about
	// the field being present.
	require.NoError(t, q.Send(t.Context(), sendGrant("shop/prod/QmRaft", map[string]string{
		settingQueueConsensus: "raft",
	}), "1", []byte("x")))
}
