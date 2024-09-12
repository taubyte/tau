package repositoryLib

import (
	"fmt"
	"os"
	"path"
	"strings"

	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/singletons/config"
)

func (info *Info) isCloned(repositoryPath string) bool {
	_, err := os.Stat(repositoryPath)

	return err == nil
}

// Get the repository path from info provided
func (info *Info) path(project config.Project) (string, error) {
	// Check to confirm full name is valid
	if len(info.FullName) == 0 {
		if len(info.ID) > 0 {
			err := info.GetNameFromID()
			if err != nil {
				return "", err
			}
		} else {
			return "", fmt.Errorf("repository fullname or ID not provided for type %s", info.Type)
		}
	}

	// Confirm fullName is valid
	splitName := strings.Split(info.FullName, "/")
	if len(splitName) == 1 {
		profile, err := loginLib.GetSelectedProfile()
		if err != nil {
			return "", err
		}

		// Create a full name with username and repo name
		info.FullName = strings.Join([]string{profile.GitUsername, splitName[0]}, "/")
	}

	var loc string
	switch info.Type {
	case WebsiteRepositoryType:
		loc = project.WebsiteLoc()
	case LibraryRepositoryType:
		loc = project.LibraryLoc()
	}

	return path.Join(loc, strings.Split(info.FullName, "/")[1]), nil
}
