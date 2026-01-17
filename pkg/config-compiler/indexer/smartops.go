package indexer

import (
	"fmt"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	smartOpSpec "github.com/taubyte/tau/pkg/specs/smartops"
	"github.com/taubyte/tau/utils/maps"
)

func SmartOps(ctx *IndexContext, project projectSchema.Project, urlIndex map[string]interface{}) error {
	if urlIndex == nil {
		return fmt.Errorf("urlIndex received is nil")
	}

	if ctx.Obj == nil {
		return fmt.Errorf("obj received is nil")
	}

	if ctx.Commit == "" || ctx.Branch == "" || ctx.ProjectId == "" {
		return fmt.Errorf("commit, branch, and project required for IndexContext: `%v`", ctx)
	}

	smartOpObj, ok := ctx.Obj[string(smartOpSpec.PathVariable)]
	if !ok {
		return nil // This shouldn't be breaking,  it just means there are no smartOps
	}

	for _, smartOp := range maps.SafeInterfaceToStringKeys(smartOpObj) {
		name, err := maps.String(maps.SafeInterfaceToStringKeys(smartOp), "name")
		if err != nil {
			return err
		}

		_smart, err := project.SmartOps(name, ctx.AppName)
		if err != nil {
			return err
		}

		if len(_smart.Get().Id()) == 0 {
			return fmt.Errorf("SmartOp `%s` not found", _smart.Get().Name())
		}

		getter := _smart.Get()
		tnsPath, err := smartOpSpec.Tns().IndexValue(ctx.Branch, ctx.ProjectId, ctx.AppId, getter.Id())
		if err != nil {
			return err
		}

		indexPath, err := smartOpSpec.Tns().WasmModulePath(ctx.ProjectId, ctx.AppId, name)
		if err != nil {
			return err
		}

		linksPath := indexPath.Versioning().Links().String()
		if _, exists := urlIndex[linksPath]; !exists {
			urlIndex[linksPath] = make([]string, 0)
		}

		skip := false
		for _, val := range urlIndex[linksPath].([]string) {
			if tnsPath.String() == val {
				skip = true
			}
		}

		if !skip {
			urlIndex[linksPath] = append(urlIndex[linksPath].([]string), tnsPath.String())
		}
	}

	return nil
}
