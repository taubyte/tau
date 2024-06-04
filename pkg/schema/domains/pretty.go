package domains

import "github.com/taubyte/tau/pkg/schema/pretty"

func (d *domain) Prettify(pretty.Prettier) map[string]interface{} {
	getter := d.Get()
	return map[string]interface {
	}{
		"Id":             getter.Id(),
		"Name":           getter.Name(),
		"Description":    getter.Description(),
		"Tags":           getter.Tags(),
		"FQDN":           getter.FQDN(),
		"UseCertificate": getter.UseCertificate(),
		"Type":           getter.Type(),

		// Currently unused, as we don't yet support it.  May not want this displayed even if we did support it.
		// "Cert":           getter.Cert(),
		// "Key":            getter.Key(),
	}
}
