package basic

import (
	"github.com/taubyte/go-seer"
)

// Set runs provided ops and writes the the config if sync is true, returns an error
func (r Resource) Set(sync bool, ops ...Op) (err error) {
	queries := make([]*seer.Query, 0)
	for _, op := range ops {
		queries = append(queries, op(r.ResourceIface)...)
	}

	err = r.seer.Batch(queries...).Commit()
	if err != nil {
		return r.WrapError("commit failed with: %s", err)
	}

	if sync {
		err = r.seer.Sync()
		if err != nil {
			return r.WrapError("sync failed with: %s", err)
		}
	}

	return nil
}

// Set takes a value and a name to generate an Op for setting the values in config
func Set(name string, value interface{}) Op {
	return func(c ConfigIface) []*seer.Query {
		return []*seer.Query{c.Config().Get(name).Set(value)}
	}
}

func SetChild(parent string, name string, value interface{}) Op {
	return func(c ConfigIface) []*seer.Query {
		return []*seer.Query{c.Config().Get(parent).Get(name).Set(value)}
	}
}
