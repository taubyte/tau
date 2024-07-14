package decompile

import (
	"fmt"
	"os"

	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/utils/id"
)

type buildContext struct {
	projectId string
	dir       string
	project   project.Project
}

func generateId(_id string) string {
	if len(_id) > 0 {
		return _id
	} else {
		return id.Generate(_id)
	}
}

// Takes a slice of structureSpec Structures and converts them into the project
func MockBuild(projectId string, dir string, ifaces ...interface{}) (project.Project, error) {
	ctx := &buildContext{projectId: projectId, dir: dir}

	err := ctx.newProject()
	if err != nil {
		return nil, err
	}

	if err := ctx.newStructs(ifaces...); err != nil {
		return nil, err
	}

	return ctx.project, nil
}

func (ctx *buildContext) newProject() (err error) {
	if ctx.dir == "" {
		ctx.dir, err = os.MkdirTemp(os.TempDir(), "project-*")
		if err != nil {
			return
		}
	}

	err = os.MkdirAll(ctx.dir, 0750)
	if err != nil {
		return fmt.Errorf("creating tx.dir %s failed with: %v", ctx.dir, err)
	}

	ctx.project, err = project.Open(project.SystemFS(ctx.dir))
	if err != nil {
		return fmt.Errorf("project.Open failed with: %v", err)
	}

	err = ctx.project.Set(
		true,
		project.Id(ctx.projectId),
		project.Name("builtProject"),
	)
	if err != nil {
		return fmt.Errorf("p.set failed with: %v", err)
	}

	return
}

func (ctx *buildContext) newStructs(ifaces ...interface{}) (err error) {
	for _, iface := range ifaces {
		if err = ctx.newStruct(iface); err != nil {
			return
		}
	}
	return
}

func (ctx *buildContext) newStruct(iface interface{}) (err error) {
	switch v := iface.(type) {
	case *structureSpec.Function:
		return function(ctx.project, generateId(v.Id), iface, "")
	case *structureSpec.Messaging:
		return messaging(ctx.project, generateId(v.Id), iface, "")
	case *structureSpec.Domain:
		return domain(ctx.project, generateId(v.Id), iface, "")
	case *structureSpec.Database:
		return database(ctx.project, generateId(v.Id), iface, "")
	case *structureSpec.Storage:
		return storage(ctx.project, generateId(v.Id), iface, "")
	case *structureSpec.Service:
		return service(ctx.project, generateId(v.Id), iface, "")
	case *structureSpec.Library:
		return library(ctx.project, generateId(v.Id), iface, "")
	case *structureSpec.SmartOp:
		return smartop(ctx.project, generateId(v.Id), iface, "")
	case *structureSpec.Website:
		return website(ctx.project, generateId(v.Id), iface, "")
	case []interface{}:
		for _, _iface := range iface.([]interface{}) {
			err = ctx.newStruct(_iface)
			if err != nil {
				return
			}
		}
	default:
		err = fmt.Errorf("struct `%T` not yet supported", iface)
	}
	return
}
