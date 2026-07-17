package basic

import seer "github.com/taubyte/tau/pkg/yaseer"

// Can be overridden
func (r Resource) config() *seer.Query {
	return r.Root().Document()
}

// Can be overridden
func (r Resource) root() *seer.Query {
	if len(r.AppName()) == 0 {
		return r.seer.Get(r.Directory()).Get(r.Name())
	}

	return r.seer.Get("applications").Get(r.AppName()).Get(r.Directory()).Get(r.Name())
}
