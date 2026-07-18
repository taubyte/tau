package interp

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp/utils"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// AttachAll implements the AttachesToAll() cross-cutting attachment at compile
// time. The group marked AttachesToAll (smartops) contributes a derived wire
// field to every OTHER resource: the ids of that group's instances named in the
// resource's `tags` under the "<groupKey>:" prefix (e.g. a function tag
// "smartops:foo" adds foo's smartop id to the function's "smartops" list).
//
// It reproduces the frozen compiler's attachSmartOpsFromTags
// (config-compiler/compile/common.go): a "<groupKey>:<name>" tag resolves <name>
// to the group's id (app-local then project-root scope) and the resolved ids are
// written to the resource's "<groupKey>" key ONLY when the list is non-empty; the
// tags themselves are left in place. It is driven entirely by the AttachesToAll
// annotation, so it is not smartops-specific.
//
// Wrapped in utils.Global so it runs at project scope and each application scope
// (matching the frozen scope rules), and it MUST run after the CompileDriver has
// populated the name->id index — a group's names only resolve once it is indexed.
func AttachAll(root *engine.Node) transform.Transformer[object.Refrence] {
	return utils.Global(&attachAll{groups: root.Children})
}

type attachAll struct {
	groups []*engine.Node
}

func (a *attachAll) Process(ct transform.Context[object.Refrence], scope object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	attachKey := attachesToAllKey(a.groups)
	if attachKey == "" {
		return scope, nil
	}
	prefix := attachKey + ":"

	for _, g := range a.groups {
		groupKey, _ := g.Match.(string)
		// A resource never attaches the cross-cutting group to itself (the frozen
		// compiler calls attachSmartOpsFromTags for every resource except smartops).
		if groupKey == attachKey || len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		// Only tagged resource groups can carry the "<groupKey>:" tags; the
		// applications container and clouds have no Resource descriptor.
		if _, isResource := iter.Meta["resource"].([4]string); !isResource {
			continue
		}
		if !hasAttr(iter, "tags") {
			continue
		}

		groupObj, err := scope.Child(groupKey).Object()
		if err == object.ErrNotExist {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("fetching %s failed with %w", groupKey, err)
		}

		for _, instName := range groupObj.Children() {
			if err := attachFromTags(ct, groupObj.Child(instName), attachKey, prefix); err != nil {
				return nil, fmt.Errorf("%s %s: %w", groupKey, instName, err)
			}
		}
	}
	return scope, nil
}

// attachesToAllKey returns the config key of the group marked AttachesToAll, or ""
// if none is (there is at most one).
func attachesToAllKey(groups []*engine.Node) string {
	for _, g := range groups {
		if len(g.Children) == 0 {
			continue
		}
		if b, _ := g.Children[0].Meta["attachesToAll"].(bool); b {
			key, _ := g.Match.(string)
			return key
		}
	}
	return ""
}

// attachFromTags reads one resource's compiled `tags`, resolves every
// "<prefix><name>" tag to the AttachesToAll group's id (app-local then
// project-root scope, via ResolveNamesToId) and writes the id list to the
// resource's attachKey wire field when non-empty. Mirrors attachSmartOpsFromTags:
// the set happens only for a non-empty list and the tags are not modified.
func attachFromTags(ct transform.Context[object.Refrence], sel object.Selector[object.Refrence], attachKey, prefix string) error {
	val, err := sel.Get("tags")
	if err != nil || val == nil {
		return nil
	}
	tags, ok := val.([]string)
	if !ok {
		return nil
	}

	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag == "" || !strings.HasPrefix(tag, prefix) {
			continue
		}
		names = append(names, strings.TrimPrefix(tag, prefix))
	}
	if len(names) == 0 {
		return nil
	}

	ids, err := utils.ResolveNamesToId(ct, attachKey, names)
	if err != nil {
		return fmt.Errorf("resolving %s tags failed with %w", attachKey, err)
	}
	if len(ids) == 0 {
		return nil
	}
	return sel.Set(attachKey, ids)
}
