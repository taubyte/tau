package codefile

import (
	"path"

	"github.com/taubyte/tau/tools/tau/config"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
)

// applicationFolder is where an application scopes its code, mirroring the
// config repo layout.
const applicationFolder = "applications"

func Path(name, folder string) (CodePath, error) {
	projectConfig, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return "", err
	}

	application, _ := config.GetSelectedApplication()

	var codePath string
	if len(application) > 0 {
		codePath = path.Join(projectConfig.CodeLoc(), applicationFolder, application, folder, name)
	} else {
		codePath = path.Join(projectConfig.CodeLoc(), folder, name)
	}

	return CodePath(codePath), nil
}
