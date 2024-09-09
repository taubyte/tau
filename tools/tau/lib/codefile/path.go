package codefile

import (
	"path"

	schemaCommon "github.com/taubyte/tau/pkg/schema/common"
	"github.com/taubyte/tau/tools/tau/env"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
)

func Path(name, folder string) (CodePath, error) {
	projectConfig, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return "", err
	}

	application, _ := env.GetSelectedApplication()

	var codePath string
	if len(application) > 0 {
		codePath = path.Join(projectConfig.CodeLoc(), schemaCommon.ApplicationFolder, application, folder, name)
	} else {
		codePath = path.Join(projectConfig.CodeLoc(), folder, name)
	}

	return CodePath(codePath), nil
}
