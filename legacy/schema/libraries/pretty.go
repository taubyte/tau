package libraries

import (
	"github.com/taubyte/tau/legacy/schema/pretty"
	"github.com/taubyte/tau/legacy/specs/methods"
)

// Prettify renders the library and, when a prettifier is given, resolves the
// build asset it published.
//
// The asset is addressed by the LIBRARY's id, not by the id of the git
// repository behind it: monkey stashes a library build under the resource id it
// resolved from the repository index (handleCompressedBuild), and the substrate
// resolves it back by the same id. Reading it under the git id — which is what
// shadowing `id` with the second value of Git() used to do — hashes to a key
// nothing was ever written to, so the library alone reported Error where every
// other kind reported Asset, and its own project was told it had published
// nothing.
func (l *library) Prettify(p pretty.Prettier) map[string]interface{} {
	getter := l.Get()
	provider, gitID, fullName := getter.Git()

	id := getter.Id()
	obj := map[string]interface {
	}{
		"Id":          id,
		"Name":        getter.Name(),
		"Description": getter.Description(),
		"Tags":        getter.Tags(),
		"Path":        getter.Path(),
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
