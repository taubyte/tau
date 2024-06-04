package smartops

import "github.com/taubyte/tau/pkg/schema/pretty"

func (s *smartOps) Prettify(pretty.Prettier) map[string]interface{} {
	getter := s.Get()

	return map[string]interface {
	}{
		"Id":          getter.Id(),
		"Name":        getter.Name(),
		"Description": getter.Description(),
		"Tags":        getter.Tags(),
		"Source":      getter.Source(),
		"Timeout":     getter.Timeout(),
		"Memory":      getter.Memory(),
		"Call":        getter.Call(),
	}
}
