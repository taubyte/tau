package dreamLib

import (
	"strconv"
)

type BuildLocalConfigCode struct {
	Config      bool
	Code        bool
	Branch      string
	ProjectPath string
	ProjectID   string
}

func (b BuildLocalConfigCode) Execute() error {
	return Execute([]string{
		"inject", "buildLocalProject",
		"--config", strconv.FormatBool(b.Config),
		"--code", strconv.FormatBool(b.Code),
		"--branch", b.Branch,
		"--path", b.ProjectPath,
		"--project-id", b.ProjectID,
	}...)
}
