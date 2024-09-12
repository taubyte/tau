package smartopsLib

import (
	"os"

	schemaCommon "github.com/taubyte/tau/pkg/schema/common"
	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/lib/codefile"
)

func New(smartops *structureSpec.SmartOp, templateURL string) error {
	_, err := set(smartops, true)
	if err != nil {
		return err
	}

	codePath, err := codefile.Path(smartops.Name, schemaCommon.SmartOpsFolder)
	if err != nil {
		return err
	}

	return codePath.Write(templateURL, smartops.Name)
}

func Set(smartops *structureSpec.SmartOp) (err error) {
	_, err = set(smartops, false)
	return
}

func Delete(name string) error {
	info, err := get(name)
	if err != nil {
		return err
	}

	err = info.smartops.Delete()
	if err != nil {
		return err
	}

	codePath, err := codefile.Path(name, schemaCommon.SmartOpsFolder)
	if err != nil {
		return err
	}

	return os.RemoveAll(codePath.String())
}

func List() ([]string, error) {
	_, _, smartops, err := list()
	if err != nil {
		return nil, err
	}

	return smartops, nil
}

func ListResources() ([]*structureSpec.SmartOp, error) {
	project, application, relative, err := list()
	if err != nil {
		return nil, err
	}

	smartops := make([]*structureSpec.SmartOp, len(relative))
	for idx, name := range relative {
		_smartops, err := project.SmartOps(name, application)
		if err != nil {
			return nil, err
		}

		smartops[idx], err = _smartops.Get().Struct()
		if err != nil {
			return nil, err
		}
	}

	return smartops, nil
}

func ProjectSmartOpCount(project project.Project) (smartopsCount int) {
	_, global := project.Get().SmartOps("")
	smartopsCount += len(global)

	for _, app := range project.Get().Applications() {
		local, _ := project.Get().SmartOps(app)
		smartopsCount += len(local)
	}

	return
}
