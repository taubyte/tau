package build

import (
	"errors"
	"os"
	"path"
	"strings"

	commonSpec "github.com/taubyte/tau/pkg/specs/common"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	"github.com/taubyte/tau/tools/tau/config"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
)

var errInvalidRepoName = errors.New("invalid repository name: expected \"user/repo\"")

// verifyWorkDirExists returns an error if workDir does not exist or is not a directory.
func verifyWorkDirExists(workDir string) error {
	info, err := os.Stat(workDir)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("resource not cloned locally: " + workDir)
		}
		return err
	}
	if !info.IsDir() {
		return errors.New("path is not a directory: " + workDir)
	}
	return nil
}

// buildContext holds project config and selected app for local build (no branch).
type buildContext struct {
	projectConfig config.Project
	selectedApp   string
}

// getBuildContext returns project config and selected app from local config.
func getBuildContext() (*buildContext, error) {
	projectConfig, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return nil, err
	}
	selectedApp, _ := config.GetSelectedApplication()
	return &buildContext{
		projectConfig: projectConfig,
		selectedApp:   selectedApp,
	}, nil
}

// workDirForFunction returns the local path for the given function.
func (c *buildContext) workDirForFunction(functionName string) string {
	if len(c.selectedApp) > 0 {
		return path.Join(c.projectConfig.CodeLoc(), commonSpec.ApplicationPathVariable.String(), c.selectedApp, functionSpec.PathVariable.String(), functionName)
	}
	return path.Join(c.projectConfig.CodeLoc(), functionSpec.PathVariable.String(), functionName)
}

// workDirForWebsite returns the local path for the website repo (repoName is "user/repo", we use the second segment).
func (c *buildContext) workDirForWebsite(repoName string) (string, error) {
	split := strings.Split(repoName, "/")
	if len(split) != 2 {
		return "", errInvalidRepoName
	}
	return path.Join(c.projectConfig.WebsiteLoc(), split[1]), nil
}

// workDirForLibrary returns the local path for the library repo.
func (c *buildContext) workDirForLibrary(repoName string) (string, error) {
	split := strings.Split(repoName, "/")
	if len(split) != 2 {
		return "", errInvalidRepoName
	}
	return path.Join(c.projectConfig.LibraryLoc(), split[1]), nil
}
