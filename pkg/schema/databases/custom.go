package databases

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

// Custom accessors with value transforms the generator can't derive.
// tcc-gen deliberately skips these fields (skipBoth in tools/tcc-gen); keep
// them here so regenerating getter.go/set.go doesn't drop them.

func (g getter) Local() bool {
	network := basic.Get[string](g, "access", "network")
	return (network == "host")
}

func (d getter) Secret() bool {
	var val struct{}
	return (d.Config().Get("encryption").Value(&val) == nil)
}

func (g getter) Encryption() (key string, keyType string) {
	enc := g.Config().Get("encryption")

	enc.Fork().Get("key").Value(&key)
	enc.Fork().Get("type").Value(&keyType)

	return
}

func Local(value bool) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		var access string
		if value {
			access = "host"
		} else {
			access = "all"
		}
		return []*seer.Query{
			c.Config().Get("access").Get("network").Set(access),
		}
	}
}

func getEncryptionTypeAndKey(arg string) (_type string, key string) {

	// Get from filepath or raw
	key = arg

	// Set _type
	_type = "AES512"
	return
}

func Encryption(key string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		_type, key := getEncryptionTypeAndKey(key)
		encryption := c.Config().Get("encryption")
		return []*seer.Query{
			encryption.Fork().Get("type").Set(_type),
			encryption.Fork().Get("key").Set(key),
		}
	}
}

func Storage(size string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		return []*seer.Query{
			c.Config().Get("storage").Get("size").Set(size),
		}
	}
}
