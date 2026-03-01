package build

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	commonSpec "github.com/taubyte/tau/pkg/specs/common"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	"github.com/taubyte/tau/tools/tau/common"
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

// buildsDirForFunction returns the builds directory path for the given function
// (mirrors workDirForFunction under builds/ instead of code/).
func buildsDirForFunction(projectLocation, app, functionName string) string {
	if len(app) > 0 {
		return path.Join(projectLocation, common.BuildsDir, commonSpec.ApplicationPathVariable.String(), app, functionSpec.PathVariable.String(), functionName)
	}
	return path.Join(projectLocation, common.BuildsDir, functionSpec.PathVariable.String(), functionName)
}

// ResolveArtifactPath returns the path to artifact.zip or main.wasm under the
// function's builds dir if either exists, otherwise an error.
func ResolveArtifactPath(projectLocation, app, functionName string) (string, error) {
	dir := buildsDirForFunction(projectLocation, app, functionName)
	for _, name := range []string{wasm.ZipFile, wasm.WasmFile} {
		candidate := path.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("artifact not found in %s (run \"tau build function\" first or set --wasm)", dir)
}

// SourceDirForFunction returns the source code directory path for the given function
// (code repo path: code/.../functions/<name> or code/apps/<app>/functions/<name>).
func SourceDirForFunction(projectLocation, app, functionName string) string {
	if len(app) > 0 {
		return path.Join(projectLocation, common.CodeRepoDir, commonSpec.ApplicationPathVariable.String(), app, functionSpec.PathVariable.String(), functionName)
	}
	return path.Join(projectLocation, common.CodeRepoDir, functionSpec.PathVariable.String(), functionName)
}

// LatestModTimeInDir returns the latest modification time of any file under dir (recursive).
// If dir does not exist or has no regular files, returns zero time and nil error.
func LatestModTimeInDir(dir string) (time.Time, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	if !info.IsDir() {
		return time.Time{}, fmt.Errorf("not a directory: %s", dir)
	}
	var latest time.Time
	err = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return latest, nil
}

// IsArtifactStale reports whether the artifact at artifactPath is older than the
// latest modified file in sourceDir. If sourceDir does not exist or has no files, returns false.
func IsArtifactStale(artifactPath, sourceDir string) (bool, error) {
	artifactInfo, err := os.Stat(artifactPath)
	if err != nil {
		return false, err
	}
	latestSource, err := LatestModTimeInDir(sourceDir)
	if err != nil {
		return false, err
	}
	if latestSource.IsZero() {
		return false, nil
	}
	return artifactInfo.ModTime().Before(latestSource), nil
}
