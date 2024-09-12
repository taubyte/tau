package repositoryLib

import (
	"errors"
	"os"
	"path"
	"strings"

	"github.com/taubyte/tau/pkg/git"
	libraryI18n "github.com/taubyte/tau/tools/tau/i18n/library"
	websiteI18n "github.com/taubyte/tau/tools/tau/i18n/website"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/taubyte/tau/tools/tau/states"
)

func (info *Info) HasBeenCloned(project config.Project, provider string) bool {
	var dir string
	switch info.Type {
	case WebsiteRepositoryType:
		dir = project.WebsiteLoc()
	case LibraryRepositoryType:
		dir = project.LibraryLoc()
	default:
		return false

	}

	repositoryPath := path.Join(dir, strings.Split(info.FullName, "/")[1])

	_, err := os.Stat(repositoryPath)
	return err == nil
}

func (info *Info) Clone(project config.Project, url, branch string, embedded bool) (*git.Repository, error) {
	if !info.DoClone {
		return nil, errors.New("cloning when info.Clone is false")
	}

	repositoryPath, err := info.path(project)
	if err != nil {
		return nil, err
	}

	_, err = os.Stat(repositoryPath)
	if err == nil {
		if info.Type == WebsiteRepositoryType {
			websiteI18n.Help().WebsiteAlreadyCloned(repositoryPath)
			return nil, websiteI18n.ErrorAlreadyCloned
		}

		libraryI18n.Help().LibraryAlreadyCloned(repositoryPath)
		return nil, libraryI18n.ErrorAlreadyCloned
	}

	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return nil, err
	}

	var tokenOption git.Option
	if embedded {
		tokenOption = git.EmbeddedToken(profile.Token)
	} else {
		tokenOption = git.Token(profile.Token)
	}

	repo, err := git.New(states.Context,
		git.Root(repositoryPath),
		git.Author(profile.GitUsername, profile.GitEmail),
		git.URL(url),
		tokenOption,

		// TODO branch, this breaks things
		// git.Branch(branch),
	)
	if err != nil {
		return nil, err
	}

	return repo, nil
}
