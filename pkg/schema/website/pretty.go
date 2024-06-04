package website

import (
	"github.com/taubyte/tau/pkg/schema/pretty"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func (w *website) Prettify(p pretty.Prettier) map[string]interface{} {
	getter := w.Get()

	provider, gitID, fullName := getter.Git()

	id := getter.Id()
	obj := map[string]interface {
	}{
		"Id":          id,
		"Name":        getter.Name(),
		"Description": getter.Description(),
		"Tags":        getter.Tags(),
		"Domains":     getter.Domains(),
		"Paths":       getter.Paths(),
		"Branch":      getter.Branch(),
		"GitProvider": provider,
		"GitId":       gitID,
		"GitFullName": fullName,
	}

	if p == nil {
		return obj
	}

	tnsPath, err := methods.GetTNSAssetPath(p.Project(), id, p.Branch())
	if err != nil {
		obj["Error"] = err
		return obj
	}

	assetCid, err := p.Fetch(tnsPath)
	if err != nil {
		obj["Error"] = err
		return obj
	}

	obj["Asset"] = assetCid.Interface()
	return obj
}
