package storages

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

// Custom accessors with value transforms the generator can't derive.
// tcc-gen deliberately skips these fields (skipBoth in tools/tcc-gen); keep
// them here so regenerating getter.go/set.go doesn't drop them.

func (g getter) Type() string {
	var val struct{}
	for _, _type := range []string{"object", "streaming"} {
		if g.Config().Get(_type).Value(&val) == nil {
			return _type
		}
	}

	return ""
}

func (g getter) Public() bool {
	return basic.Get[string](g, "access", "network") == "all"
}

func (g getter) Size() string {
	return basic.Get[string](g, g.Type(), "size")
}

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
