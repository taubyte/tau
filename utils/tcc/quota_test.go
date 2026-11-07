package tccUtils

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/platformerr"
)

func TestCheckQuota(t *testing.T) {
	obj := map[string]any{
		"databases": map[string]any{"d1": map[string]any{"name": "db1", "size": 600}},
		"applications": map[string]any{"a": map[string]any{
			"storages": map[string]any{"s1": map[string]any{"name": "st1", "size": uint64(600)}},
		}},
	}
	for name, c := range map[string]struct {
		caps config.Caps
		bad  bool
	}{
		"unlimited":  {config.Caps{}, false},
		"under":      {config.Caps{StorageBytes: 1200, Resources: 2}, false},
		"over total": {config.Caps{StorageBytes: 1000}, true},
		"over count": {config.Caps{Resources: 1}, true},
	} {
		err := CheckQuota(obj, c.caps)
		if (err != nil) != c.bad {
			t.Fatalf("%s: %v", name, err)
		}
		if c.bad && platformerr.CodeOf(err) != platformerr.OverQuota {
			t.Fatalf("%s: code %q", name, platformerr.CodeOf(err))
		}
	}
	if err := CheckQuota(obj, config.Caps{StorageBytes: 1000}); err == nil || !strings.Contains(err.Error(), `"st1"`) || !strings.Contains(err.Error(), "1000") {
		t.Fatalf("message must name the resource and the cap: %v", err)
	}
}
