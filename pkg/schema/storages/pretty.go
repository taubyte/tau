package storages

import "github.com/taubyte/tau/pkg/schema/pretty"

func (s *storage) Prettify(pretty.Prettier) map[string]interface{} {
	getter := s.Get()

	_type := getter.Type()
	obj := map[string]interface {
	}{
		"Id":          getter.Id(),
		"Name":        getter.Name(),
		"Description": getter.Description(),
		"Tags":        getter.Tags(),
		"Match":       getter.Match(),
		"Regex":       getter.Regex(),
		"Size":        getter.Size(),
		"Type":        _type,
	}

	switch _type {
	case "object":
		obj["Public"] = getter.Public()
		obj["Versioning"] = getter.Versioning()
	case "streaming":
		obj["TTL"] = getter.TTL()
	}

	return obj
}
