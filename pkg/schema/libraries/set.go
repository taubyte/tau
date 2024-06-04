package libraries

import (
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
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

func Path(value string) basic.Op {
	return basic.SetChild("source", "path", value)
}

func Branch(value string) basic.Op {
	return basic.SetChild("source", "branch", value)
}

func Github(id string, fullname string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		base := c.Config().Get("source").Get("github")
		return []*seer.Query{
			base.Fork().Get("id").Set(id),
			base.Fork().Get("fullname").Set(fullname),
		}
	}
}

func SmartOps(value []string) basic.Op {
	return basic.Set("smartops", value)
}
