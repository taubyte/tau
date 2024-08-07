package libraries

import (
	"github.com/taubyte/tau/pkg/schema/pretty"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func (l *library) Prettify(p pretty.Prettier) map[string]interface{} {
	getter := l.Get()
	provider, id, fullName := getter.Git()
	obj := map[string]interface {
	}{
		"Id":          getter.Id(),
		"Name":        getter.Name(),
		"Description": getter.Description(),
		"Tags":        getter.Tags(),
		"Path":        getter.Path(),
		"Branch":      getter.Branch(),
		"GitProvider": provider,
		"GitId":       id,
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
