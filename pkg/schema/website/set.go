package website

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

func Domains(value []string) basic.Op {
	return basic.Set("domains", value)
}

func Paths(value []string) basic.Op {
	return basic.SetChild("source", "paths", value)
}

func Branch(value string) basic.Op {
	return basic.SetChild("source", "branch", value)
}

func Github(id string, fullname string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		provider := "github"
		base := c.Config().Get("source").Get(provider)
		return []*seer.Query{
			base.Fork().Get("id").Set(id),
			base.Fork().Get("fullname").Set(fullname),
		}
	}
}

func SmartOps(value []string) basic.Op {
	return basic.Set("smartops", value)
}
