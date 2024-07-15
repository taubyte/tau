package templates

import (
	"os"
	"strings"

	"github.com/taubyte/tau/pkg/cli/states"
	"github.com/taubyte/tau/pkg/git"
)

func loadTemplates() error {
	_, err := os.Stat(templateFolder)
	if err != nil {
		err = os.Mkdir(templateFolder, 0755)
		if err != nil {
			// TODO verbose
			return err
		}
	}

	_templates = &templates{}

	_templates.repository, err = git.New(states.Context,
		git.Root(templateRepositoryFolder),
		git.URL(TemplateRepoURL),
	)
	if err != nil {
		// TODO verbose
		return err
	}

	err = _templates.repository.Pull()
	if err != nil && !strings.Contains(err.Error(), "already up-to-date") {
		// TODO verbose
		return err
	}

	return nil
}
