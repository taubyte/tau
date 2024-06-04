package indexer

import (
	"errors"
	"fmt"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	storageSpec "github.com/taubyte/tau/pkg/specs/storage"
	"github.com/taubyte/utils/maps"
)

func Storages(ctx *IndexContext, project projectSchema.Project, urlIndex map[string]interface{}) error {
	if urlIndex == nil {
		return errors.New("urlIndex received is nil")
	}

	if ctx.Obj == nil {
		return errors.New("obj received is nil")
	}

	if ctx.Commit == "" || ctx.Branch == "" || ctx.ProjectId == "" {
		return fmt.Errorf("commit, branch, and project required for IndexContext: `%v`", ctx)
	}

	storObj, ok := ctx.Obj[string(storageSpec.PathVariable)]
	if !ok {
		return nil // This shouldn't be breaking,  it just means there are no storages
	}

	for _, storage := range maps.SafeInterfaceToStringKeys(storObj) {
		name, err := maps.String(maps.SafeInterfaceToStringKeys(storage), "name")
		if err != nil {
			return err
		}

		store, err := project.Storage(name, ctx.AppName)
		if err != nil {
			return err
		}

		getter := store.Get()
		if len(getter.Id()) == 0 {
			return fmt.Errorf("storage `%s` not found", store.Get().Name())
		}

		indexPath := storageSpec.Tns().IndexPath(ctx.ProjectId, ctx.AppId, getter.Name())
		tnsPath, err := storageSpec.Tns().IndexValue(ctx.Branch, ctx.ProjectId, ctx.AppId, getter.Id())
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

	}

	return nil
}
