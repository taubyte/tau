package messaging

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func Id(value string) basic.Op {
	return basic.Set("id", value)
}

func Channel(regex bool, match string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		channel := c.Config().Get("channel")
		return []*seer.Query{
			channel.Fork().Get("regex").Set(regex),
			channel.Fork().Get("match").Set(match),
		}
	}
}

func Bridges(mqtt bool, websocket bool) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		bridges := c.Config().Get("bridges")
		return []*seer.Query{
			bridges.Fork().Get("mqtt").Get("enable").Set(mqtt),
			bridges.Fork().Get("websocket").Get("enable").Set(websocket),
		}
	}
}

func Description(value string) basic.Op {
	return basic.Set("description", value)
}

func Tags(value []string) basic.Op {
	return basic.Set("tags", value)
}

func Local(value bool) basic.Op {
	return basic.Set("local", value)
}

func SmartOps(value []string) basic.Op {
	return basic.Set("smartops", value)
}
