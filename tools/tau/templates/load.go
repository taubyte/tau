package templates

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/git"
)

const defaultTemplateGitTimeout = 60 * time.Second

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

	ctx, cancel := context.WithTimeout(context.Background(), defaultTemplateGitTimeout)
	defer cancel()
	_templates.repository, err = git.New(ctx,
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
