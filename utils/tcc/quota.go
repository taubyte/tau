package tccUtils

import (
	"fmt"
	"sort"

	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/platformerr"
)

// CheckQuota refuses a compiled config whose databases and storages (project and
// applications together) are more numerous or larger in total than caps allow.
// Zero caps are unlimited. Function memory is not checked here.
func CheckQuota(object map[string]any, caps config.Caps) error {
	var count, total uint64
	var err error
	var walk func(map[string]any)
	walk = func(scope map[string]any) {
		for _, kind := range []string{"databases", "storages"} {
			items, _ := scope[kind].(map[string]any)
			ids := make([]string, 0, len(items))
			for id := range items {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				res, _ := items[id].(map[string]any)
				name, _ := res["name"].(string)
				size := toUint(res["size"])
				count++
				total += size
				switch {
				case err != nil:
				case caps.Resources != 0 && count > caps.Resources:
					err = fmt.Errorf("%s %q: %d databases and storages is over the limit of %d", kind[:len(kind)-1], name, count, caps.Resources)
				case caps.StorageBytes != 0 && total > caps.StorageBytes:
					err = fmt.Errorf("%s %q: %d bytes of database and storage in total is over the limit of %d bytes", kind[:len(kind)-1], name, total, caps.StorageBytes)
				}
			}
		}
		apps, _ := scope["applications"].(map[string]any)
		for _, a := range apps {
			if app, ok := a.(map[string]any); ok {
				walk(app)
			}
		}
	}
	walk(object)
	if err != nil {
		return platformerr.New(platformerr.OverQuota, err)
	}
	return nil
}

func toUint(v any) uint64 {
	switch n := v.(type) {
	case int:
		return uint64(n)
	case int64:
		return uint64(n)
	case uint64:
		return n
	case float64:
		return uint64(n)
	}
	return 0
}
