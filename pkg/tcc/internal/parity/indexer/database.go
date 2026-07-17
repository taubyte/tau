package indexer

import (
	"errors"
	"fmt"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	databaseSpec "github.com/taubyte/tau/pkg/specs/database"
	"github.com/taubyte/tau/utils/maps"
)

func Databases(ctx *IndexContext, project projectSchema.Project, urlIndex map[string]interface{}) error {
	if urlIndex == nil {
		return errors.New("urlIndex received is nil")
	}

	if ctx.Obj == nil {
		return errors.New("obj received is nil")
	}

	if ctx.Commit == "" || ctx.Branch == "" || ctx.ProjectId == "" {
		return fmt.Errorf("commit, branch, and project required for IndexContext: `%v`", ctx)
	}

	dbObj, ok := ctx.Obj[string(databaseSpec.PathVariable)]
	if !ok {
		return nil // This shouldn't be breaking,  it just means there are no databases
	}

	for _, database := range maps.SafeInterfaceToStringKeys(dbObj) {
		name, err := maps.String(maps.SafeInterfaceToStringKeys(database), "name")
		if err != nil {
			return err
		}

		db, err := project.Database(name, ctx.AppName)
		if err != nil {
			return err
		}

		id := db.Get().Id()
		if len(id) == 0 {
			return fmt.Errorf("database `%s` not found", db.Get().Name())
		}

		tnsPath, err := databaseSpec.Tns().IndexValue(ctx.Branch, ctx.ProjectId, ctx.AppId, id)
		if err != nil {
			return err
		}

		linksPath := databaseSpec.Tns().IndexPath(ctx.ProjectId, ctx.AppId, db.Get().Name()).Versioning().Links().String()
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
