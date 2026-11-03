package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance/admit"
	"github.com/taubyte/tau/instance/cache"
	nodeDatabase "github.com/taubyte/tau/instance/database"
	"github.com/taubyte/tau/instance/storage"
	"github.com/taubyte/tau/instance/volume"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func TestEverySettingKeyNamesAnAttribute(t *testing.T) {
	for kind, keys := range map[string][]string{
		"cache":   append(append([]string(nil), cache.Settings...), admit.Settings...),
		"volume":  volume.Settings,
		"storage": storage.Settings,
		// One key left: every other queue and log setting is read by services/primitives from
		// the declaration; this is the consumer's own decision (instance/dispatch.go).
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
