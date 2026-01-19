package indexer

import (
	"fmt"

	"github.com/taubyte/tau/core/common"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/utils/maps"
)

func Libraries(ctx *IndexContext, project projectSchema.Project, urlIndex map[string]interface{}) error {
	if urlIndex == nil {
		return fmt.Errorf("urlIndex received is nil")
	}

	if ctx.Obj == nil {
		return fmt.Errorf("obj received is nil")
	}

	if ctx.Commit == "" || ctx.Branch == "" || ctx.ProjectId == "" {
		return fmt.Errorf("commit, branch, and project required for IndexContext: `%v`", ctx)
	}

	libObj, ok := ctx.Obj[string(librarySpec.PathVariable)]
	if !ok {
		return nil // This shouldn't be breaking,  it just means there are no libraries
	}

	for _, library := range maps.SafeInterfaceToStringKeys(libObj) {
		name, err := maps.String(maps.SafeInterfaceToStringKeys(library), "name")
		if err != nil {
			return err
		}

		lib, err := project.Library(name, ctx.AppName)
		if err != nil {
			return err
		}

		getter := lib.Get()
		if len(getter.Id()) == 0 {
			return fmt.Errorf("library `%s` not found", getter.Name())
		}

		// set repository path
		gitProvider, repoId, _ := getter.Git()
		_path, err := methods.GetRepositoryPath(gitProvider, repoId, ctx.ProjectId)
		if err != nil {
			return err
		}
		urlIndex[_path.Type().String()] = common.LibraryRepository

		libraryId := getter.Id()
		tnsPath, err := librarySpec.Tns().IndexValue(ctx.Branch, ctx.ProjectId, ctx.AppId, getter.Id())
		if err != nil {
			return err
		}

		urlIndex[_path.Resource(libraryId).String()] = tnsPath.String()
		wasmPath, err := librarySpec.Tns().WasmModulePath(ctx.ProjectId, ctx.AppId, name)
		if err != nil {
			return err
		}

		linksPath := wasmPath.Versioning().Links().String()
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

		// TODO: SPECS
		libIndex := librarySpec.Tns().NameIndex(libraryId)
		if _, exists := urlIndex[libIndex.String()]; !exists {
			urlIndex[libIndex.String()] = getter.Name()
		}
	}

	return nil
}
