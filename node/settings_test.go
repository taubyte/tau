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
// and the backing falls back to its own default. The worker's own queue backing
// was a post-mortem of exactly that — every queue opened with pkg/queue's
// defaults, and the author's visibility timeout, retry budget and dead letter
// silently stopped taking effect.
//
// ITS TESTS COULD NOT CATCH IT EITHER, which is why this one is shaped
// differently: every other test in this package builds its grants from these
// same constants, so it proves only that node agrees with itself.
func TestEverySettingKeyNamesAnAttribute(t *testing.T) {
	for kind, keys := range map[string][]string{
		"cache":   {cacheTTL, cacheSize, settingInstanceAdmission, settingInstanceLimit, settingInstanceValidator, settingInstanceScope},
		"volume":  {settingVolumeQuota, settingCheckpointFreq},
		"storage": {settingObjectType, settingObjectSize},
		// ONE KEY LEFT, AND THAT IS THE CUTOVER RATHER THAN A GAP. Every other
		// field of a queue and of a log is read by services/primitives out of
		// the DECLARATION now — nested paths off the compiled resource, not a
		// flat key off a grant — so there is nothing here to keep in step with
		// the vocabulary. What survives is the one decision that is the
		// consumer's own; see node/dispatch.go.
		"sync":     {settingRetryEvery},
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

// THE QUEUE'S OWN PROMISE IS NOW THE SERVICE'S TO KEEP. A queue's `consensus`
// is Required by the vocabulary and takes `crdt` or `raft`, and the refusal by
// name that used to live here — "this cloud serves raft queues and that one
// asks for crdt" — moved with the queue itself: services/primitives resolves it
// off the declaration and answers ErrConsensusUnserved, which its own
// declarations_test covers. A node reads no consensus field at all any more, so
// a test of one here would be a test of nothing.
