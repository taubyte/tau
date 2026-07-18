package methods

import "github.com/taubyte/tau/pkg/specs/common"

// IndexPath is the by-name index location project/<id>[/application/<app>]/<name>.
// It carries no resource-type segment, so it is identical across resource kinds
// (databases, storages) — hence it lives here rather than in each spec.
func IndexPath(projectId, appId, name string) *common.TnsPath {
	if len(appId) > 0 {
		return common.NewTnsPath([]string{common.ProjectPathVariable.String(), projectId, common.ApplicationPathVariable.String(), appId, name})
	}
	return common.NewTnsPath([]string{common.ProjectPathVariable.String(), projectId, name})
}

// NameIndex is the id-keyed name index <resourceType>/<id>: a two-segment path
// mapping a resource id to its authored name. Generic across resource kinds — the
// resource type is the only variable — so the per-resource spec (e.g. library)
// delegates here.
func NameIndex(id string, resourceType common.PathVariable) *common.TnsPath {
	return common.NewTnsPath([]string{resourceType.String(), id})
}
