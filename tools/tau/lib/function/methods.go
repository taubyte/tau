package functionLib

import (
	"os"

	schemaCommon "github.com/taubyte/tau/pkg/schema/common"
	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/lib/codefile"
)

func New(function *structureSpec.Function, templateURL string) error {
	_, err := set(function, true)
	if err != nil {
		return err
	}

	codePath, err := codefile.Path(function.Name, schemaCommon.FunctionFolder)
	if err != nil {
		return err
	}

	return codePath.Write(templateURL, function.Name)
}

func Set(function *structureSpec.Function) (err error) {
	_, err = set(function, false)
	return
}

func Delete(name string) error {
	info, err := get(name)
	if err != nil {
		return err
	}

	err = info.function.Delete()
	if err != nil {
		return err
	}

	codePath, err := codefile.Path(name, schemaCommon.FunctionFolder)
	if err != nil {
		return err
	}

	return os.RemoveAll(codePath.String())
}

func List() ([]string, error) {
	_, _, functions, err := list()
	if err != nil {
		return nil, err
	}

	return functions, nil
}

func ListResources() ([]*structureSpec.Function, error) {
	project, application, relative, err := list()
	if err != nil {
		return nil, err
	}

	functions := make([]*structureSpec.Function, len(relative))
	for idx, name := range relative {
		function, err := project.Function(name, application)
		if err != nil {
			return nil, err
		}

		functions[idx], err = function.Get().Struct()
		if err != nil {
			return nil, err
		}
	}

	return functions, nil
}

func ProjectFunctionCount(project project.Project) (functionCount int) {
	_, global := project.Get().Functions("")
	functionCount += len(global)

	for _, app := range project.Get().Applications() {
		local, _ := project.Get().Functions(app)
		functionCount += len(local)
	}

	return
}
