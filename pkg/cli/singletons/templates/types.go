package templates

import (
	"github.com/taubyte/tau/pkg/git"
)

type templates struct {
	repository *git.Repository
}

type TemplateInfo struct {
	HideURL     bool
	URL         string
	Description string
}
