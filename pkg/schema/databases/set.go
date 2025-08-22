package databases

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func Id(value string) basic.Op {
	return basic.Set("id", value)
}

func Description(value string) basic.Op {
	return basic.Set("description", value)
}

func Tags(value []string) basic.Op {
	return basic.Set("tags", value)
}

func Match(value string) basic.Op {
	return basic.Set("match", value)
}

func Regex(value bool) basic.Op {
	return basic.Set("useRegex", value)
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

// TODO implement
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

func Replicas(min int, max int) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		replicas := c.Config().Get("replicas")
		if min >= max {
			panic("min replica cannot be greater than max")
		}
		return []*seer.Query{
			replicas.Fork().Get("min").Set(min),
			replicas.Fork().Get("max").Set(max),
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

func SmartOps(value []string) basic.Op {
	return basic.Set("smartops", value)
}
