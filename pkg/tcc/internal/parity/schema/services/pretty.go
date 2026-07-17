package services

import "github.com/taubyte/tau/pkg/tcc/internal/parity/schema/pretty"

func (s *service) Prettify(pretty.Prettier) map[string]interface{} {
	getter := s.Get()

	return map[string]interface {
	}{
		"Id":          getter.Id(),
		"Name":        getter.Name(),
		"Description": getter.Description(),
		"Tags":        getter.Tags(),
		"Protocol":    getter.Protocol(),
	}
}
