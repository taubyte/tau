package messaging

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

// Custom accessors with value transforms the generator can't derive.
// tcc-gen deliberately skips these fields (skipBoth in tools/tcc-gen); keep
// them here so regenerating getter.go/set.go doesn't drop them.

func Channel(regex bool, match string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		channel := c.Config().Get("channel")
		return []*seer.Query{
			channel.Get("regex").Set(regex),
			channel.Get("match").Set(match),
		}
	}
}

func Bridges(mqtt bool, websocket bool) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		bridges := c.Config().Get("bridges")
		return []*seer.Query{
			bridges.Get("mqtt").Get("enable").Set(mqtt),
			bridges.Get("websocket").Get("enable").Set(websocket),
		}
	}
}
