package databases

import "github.com/taubyte/tau/pkg/schema/pretty"

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
		"Min":             getter.Min(),
		"Max":             getter.Max(),
		"Size":            getter.Size(),
		"Encryption-Type": encType,
	}
}
