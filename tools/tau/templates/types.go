package templates

import (
	"github.com/taubyte/tau/pkg/git"
)

type templates struct {
	repository *git.Repository

	// loadErr is why there is no repository, carried so that a caller asking
	// for templates is told rather than handed an empty answer.
	loadErr error
}

type TemplateInfo struct {
	HideURL     bool
	URL         string
	Description string
}
