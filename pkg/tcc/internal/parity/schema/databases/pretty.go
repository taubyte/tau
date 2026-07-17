package databases

import "github.com/taubyte/tau/pkg/tcc/internal/parity/schema/pretty"

func (d *database) Prettify(p pretty.Prettier) map[string]interface{} {
	getter := d.Get()
	_, encType := getter.Encryption()
	return map[string]interface {
	}{
		"Id":              getter.Id(),
		"Name":            getter.Name(),
		"Description":     getter.Description(),
		"Tags":            getter.Tags(),
		"Match":           getter.Match(),
		"Regex":           getter.Regex(),
		"Local":           getter.Local(),
		"Secret":          getter.Secret(),
		"Size":            getter.Size(),
		"Encryption-Type": encType,
	}
}
