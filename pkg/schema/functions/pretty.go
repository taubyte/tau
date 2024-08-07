package functions

import (
	"github.com/taubyte/tau/pkg/schema/pretty"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func (f *function) Prettify(p pretty.Prettier) map[string]interface{} {
	getter := f.Get()
	_type := getter.Type()

	id := getter.Id()
	obj := map[string]interface {
	}{
		"Id":          id,
		"Name":        getter.Name(),
		"Description": getter.Description(),
		"Tags":        getter.Tags(),
		"Type":        _type,
		"Source":      getter.Source(),
		"Timeout":     getter.Timeout(),
		"Memory":      getter.Memory(),
		"Call":        getter.Call(),
	}

	switch _type {
	case "http", "https":
		obj["Method"] = getter.Method()
		obj["Paths"] = getter.Paths()
		obj["Domains"] = getter.Domains()
	case "p2p":
		obj["Command"] = getter.Command()
		obj["Local"] = getter.Local()
		obj["Protocol"] = getter.Protocol()
	default:
		obj["Channel"] = getter.Channel()
		obj["Local"] = getter.Local()
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
