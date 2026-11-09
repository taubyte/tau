package hosted

import (
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/worker/admit"
	"github.com/taubyte/tau/services/worker/cache"
	nodeDatabase "github.com/taubyte/tau/services/worker/database"
	"github.com/taubyte/tau/services/worker/objects"
	"github.com/taubyte/tau/services/worker/volume"
)

func TestEverySettingKeyNamesAnAttribute(t *testing.T) {
	for kind, keys := range map[string][]string{
		"cache":   append(append([]string(nil), cache.Settings...), admit.Settings...),
		"volume":  volume.Settings,
		"storage": objects.Settings,
		// One key left: every other queue and log setting is read by services/primitives from
		// the declaration; this is the consumer's own decision (dispatch.go).
		"sync":     {settingRetryEvery},
		"channel":  {settingProtocol, settingServeKind, settingServeName, settingServeScope},
		"function": {settingAuthorizer, settingAuthorizerScope},
		"database": nodeDatabase.Settings,
	} {
		for _, key := range keys {
			require.True(t, schema.IsAttribute(kind, key),
				"%s declares no attribute %q — the vocabulary renamed it and this backing still reads the old key, so it silently falls back to a default", kind, key)
		}
	}
}
