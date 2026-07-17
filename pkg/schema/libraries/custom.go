package libraries

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

// Custom accessors with value transforms the generator can't derive.
// tcc-gen deliberately skips these fields (skipBoth in tools/tcc-gen); keep
// them here so regenerating getter.go/set.go doesn't drop them.

func (g getter) Git() (provider, id, fullname string) {
	for _, provider = range []string{"github"} {
		data := make(map[string]string)
		if g.Config().Get("source").Get(provider).Value(&data) == nil {
			id = data["id"]
			fullname = data["fullname"]
			return
		}
	}

	return "", "", ""
}

func Github(id string, fullname string) basic.Op {
	return func(c basic.ConfigIface) []*seer.Query {
		base := c.Config().Get("source").Get("github")
		return []*seer.Query{
			base.Get("id").Set(id),
			base.Get("fullname").Set(fullname),
		}
	}
}
