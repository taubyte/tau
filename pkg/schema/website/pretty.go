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

	for _, branch := range p.Branches() {
		tnsPath, err := methods.GetTNSAssetPath(p.Project(), id, branch)
		if err != nil {
			obj["Error"] = err
			continue
		}

		assetCid, err := p.Fetch(tnsPath)
		if err != nil {
			obj["Error"] = err
			continue
		}

		obj["Asset"] = assetCid.Interface()
		delete(obj, "Error")
	}

	return obj
}
