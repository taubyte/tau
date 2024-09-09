package repositoryI18n

import (
	"errors"
	"fmt"
)

var (
	registeringRepositoryFailed = "registering `%s` failed with: %s"
	unregisterRepository        = "un-registering repositories failed with: %w"
	unknownTemplate             = "unknown template `%s` must be one of `%v`"
	listRepositories            = "listing repositories for user `%s` failed with: %w"
	deleteRepository            = "deleting repository `%s` failed with: %w"
)

func RegisteringRepositoryFailed(repo string, err error) error {
	return fmt.Errorf(registeringRepositoryFailed, repo, err)
}

func ErrorUnregisterRepositories(err error) error {
	return fmt.Errorf(unregisterRepository, err)
}

func ErrorListRepositories(user string, err error) error {
	return fmt.Errorf(listRepositories, user, err)
}

func UnknownTemplate(selectedTemplate string, templates []string) error {
	return fmt.Errorf(unknownTemplate, selectedTemplate, templates)
}

func ErrorDeleteRepository(name string, err error) error {
	return fmt.Errorf(deleteRepository, name, err)
}

var ErrorAdminRights = errors.New("invalid admin rights")
