package storages

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

// if bucket type Object
func Object(versioning bool, size string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		object := c.Config().Get("object")
		return []*seer.Query{
			object.Fork().Get("versioning").Set(versioning),
			object.Fork().Get("size").Set(size),
		}
	}
}

func Streaming(ttl string, size string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		streaming := c.Config().Get("streaming")
		return []*seer.Query{
			streaming.Fork().Get("ttl").Set(ttl),
			streaming.Fork().Get("size").Set(size),
		}
	}
}

func Public(value bool) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		var access string
		if value {
			access = "all"
		} else {
			access = "host"
		}
		return []*seer.Query{
			c.Config().Get("access").Get("network").Set(access),
		}
	}
}

func SmartOps(value []string) basic.Op {
	return basic.Set("smartops", value)
}
