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

	tnsPath, err := methods.GetTNSAssetPath(p.Project(), getter.Id(), p.Branch())
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
