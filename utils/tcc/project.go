package tccUtils

import (
	"fmt"

	"github.com/spf13/afero"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/utils/id"
)

// generateId generates an ID if the provided ID is empty
func generateId(_id string) string {
	if len(_id) > 0 {
		return _id
	}
	return id.Generate("")
}

// GenerateProject creates a project in memfs with the provided resources
// It uses schema's SetWithStruct which internally uses yasser to write YAML files
// Returns both the filesystem and the project so the fs can be used for TCC compilation
func GenerateProject(projectId string, resources ...interface{}) (afero.Fs, projectLib.Project, error) {
	fs := afero.NewMemMapFs()

	prj, err := projectLib.Open(projectLib.VirtualFS(fs, "/"))
	if err != nil {
		return nil, nil, fmt.Errorf("opening project failed: %w", err)
	}

	err = prj.Set(
		true,
		projectLib.Id(projectId),
		projectLib.Name("generatedProject"),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("setting project failed: %w", err)
	}

	for _, res := range resources {
		if err := addResource(prj, res); err != nil {
			return nil, nil, err
		}
	}

	return fs, prj, nil
}

// addResource adds a resource to the project using SetWithStruct
func addResource(prj projectLib.Project, res interface{}) error {
	switch v := res.(type) {
	case *structureSpec.Function:
		id := generateId(v.Id)
		v.SetId(id)
		iFace, err := prj.Function(v.Name, "")
		if err != nil {
			return fmt.Errorf("open function `%s` failed: %w", v.Name, err)
		}
		return iFace.SetWithStruct(true, v)

	case *structureSpec.Messaging:
		id := generateId(v.Id)
		v.SetId(id)
		iFace, err := prj.Messaging(v.Name, "")
		if err != nil {
			return fmt.Errorf("open messaging `%s` failed: %w", v.Name, err)
		}
		return iFace.SetWithStruct(true, v)

	case *structureSpec.Domain:
		id := generateId(v.Id)
		v.SetId(id)
		iFace, err := prj.Domain(v.Name, "")
		if err != nil {
			return fmt.Errorf("open domain `%s` failed: %w", v.Name, err)
		}
		return iFace.SetWithStruct(true, v)

	case *structureSpec.Database:
		id := generateId(v.Id)
		v.SetId(id)
		iFace, err := prj.Database(v.Name, "")
		if err != nil {
			return fmt.Errorf("open database `%s` failed: %w", v.Name, err)
		}
		return iFace.SetWithStruct(true, v)

	case *structureSpec.Storage:
		id := generateId(v.Id)
		v.SetId(id)
		iFace, err := prj.Storage(v.Name, "")
		if err != nil {
			return fmt.Errorf("open storage `%s` failed: %w", v.Name, err)
		}
		return iFace.SetWithStruct(true, v)

	case *structureSpec.Service:
		id := generateId(v.Id)
		v.SetId(id)
		iFace, err := prj.Service(v.Name, "")
		if err != nil {
			return fmt.Errorf("open service `%s` failed: %w", v.Name, err)
		}
		return iFace.SetWithStruct(true, v)

	case *structureSpec.Library:
		id := generateId(v.Id)
		v.SetId(id)
		iFace, err := prj.Library(v.Name, "")
		if err != nil {
			return fmt.Errorf("open library `%s` failed: %w", v.Name, err)
		}
		return iFace.SetWithStruct(true, v)

	case *structureSpec.SmartOp:
		id := generateId(v.Id)
		v.SetId(id)
		iFace, err := prj.SmartOps(v.Name, "")
		if err != nil {
			return fmt.Errorf("open smart-op `%s` failed: %w", v.Name, err)
		}
		return iFace.SetWithStruct(true, v)

	case *structureSpec.Website:
		id := generateId(v.Id)
		v.SetId(id)
		iFace, err := prj.Website(v.Name, "")
		if err != nil {
			return fmt.Errorf("open website `%s` failed: %w", v.Name, err)
		}
		return iFace.SetWithStruct(true, v)

	case []interface{}:
		for _, r := range v {
			if err := addResource(prj, r); err != nil {
				return err
			}
		}
		return nil

	default:
		return fmt.Errorf("unsupported resource type: %T", res)
	}
}
