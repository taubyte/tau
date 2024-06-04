package indexer

import (
	"fmt"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	"github.com/taubyte/utils/maps"
)

func Functions(ctx *IndexContext, project projectSchema.Project, urlIndex map[string]interface{}) error {
	if urlIndex == nil {
		return fmt.Errorf("urlIndex received is nil")
	}

	if ctx.Obj == nil {
		return fmt.Errorf("obj received is nil")
	}

	if ctx.Commit == "" || ctx.Branch == "" || ctx.ProjectId == "" {
		return fmt.Errorf("commit, branch, and project required for IndexContext: `%v`", ctx)
	}

	funcObj, ok := ctx.Obj[string(functionSpec.PathVariable)]
	if !ok {
		return nil // This shouldn't be breaking,  it just means there are no functions
	}

	for _, function := range maps.SafeInterfaceToStringKeys(funcObj) {
		name, err := maps.String(maps.SafeInterfaceToStringKeys(function), "name")
		if err != nil {
			return err
		}

		_func, err := project.Function(name, ctx.AppName)
		if err != nil {
			return err
		}

		if len(_func.Get().Id()) == 0 {
			return fmt.Errorf("function `%s` not found", _func.Get().Name())
		}

		getter := _func.Get()
		tnsPath, err := functionSpec.Tns().IndexValue(ctx.Branch, ctx.ProjectId, ctx.AppId, getter.Id())
		if err != nil {
			return err
		}

		indexPath, err := functionSpec.Tns().WasmModulePath(ctx.ProjectId, ctx.AppId, name)
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
				break
			}
		}

		if !skip {
			urlIndex[linksPath] = append(urlIndex[linksPath].([]string), tnsPath.String())
		}

		_type := getter.Type()
		if _type != "http" && _type != "https" {
			continue
		}

		for _, domain := range getter.Domains() {
			domObj, err := getDomain(domain, ctx.AppName, project)
			if err != nil {
				return err
			}

			httpPath, err := functionSpec.Tns().HttpPath(domObj.Get().FQDN())
			if err != nil {
				return err
			}

			linksPath := httpPath.Versioning().Links().String()
			// create entry if empty
			if _, exists := urlIndex[linksPath]; !exists {
				urlIndex[linksPath] = make([]string, 0)
			}

			// check if value not there already
			skip := false
			for _, val := range urlIndex[linksPath].([]string) {
				if tnsPath.String() == val {
					skip = true
					break
				}
			}

			// add value (path to object) to the list
			if !skip {
				urlIndex[linksPath] = append(urlIndex[linksPath].([]string), tnsPath.String())
			}
		}

	}

	return nil
}
