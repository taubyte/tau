package internal

import (
	_ "embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"

	"github.com/otiai10/copy"
	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/schema/project"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func NewSeer() (*seer.Seer, error) {
	return seer.New(seer.VirtualFS(afero.NewMemMapFs(), "/"))
}

func NewProjectEmpty() (project.Project, error) {
	project, err := project.Open(project.VirtualFS(afero.NewMemMapFs(), "/"))
	if err != nil {
		return nil, fmt.Errorf("open project failed with error: %s", err)
	}

	return project, nil
}

func NewProjectReadOnly() (project.Project, error) {
	_, _path, _, _ := runtime.Caller(0)
	dir := filepath.Dir(_path)

	fs := afero.NewReadOnlyFs(afero.NewOsFs())

	project, err := project.Open(project.VirtualFS(fs, path.Join(dir, "config")))
	if err != nil {
		return nil, fmt.Errorf("open project failed with error: %s", err)
	}

	return project, nil
}

// If edits are made here it will change the test config
func NewProjectSystemFS() (project.Project, error) {
	_, _path, _, _ := runtime.Caller(0)
	dir := filepath.Dir(_path)

	project, err := project.Open(project.SystemFS(path.Join(dir, "config")))
	if err != nil {
		return nil, fmt.Errorf("open project failed with error: %s", err)
	}

	return project, nil
}

func NewProjectCopy() (project.Project, func(), error) {
	_, _path, _, _ := runtime.Caller(0)
	dir := filepath.Dir(_path)

	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	projectDir := path.Join(cwd, "assets")

	err = copy.Copy(path.Join(dir, "config"), projectDir)
	if err != nil {
		return nil, nil, err
	}

	fs := afero.NewOsFs()

	project, err := project.Open(project.VirtualFS(fs, projectDir))
	if err != nil {
		return nil, nil, fmt.Errorf("open project failed with error: %s", err)
	}

	close := func() {
		os.RemoveAll(projectDir)
	}

	return project, close, nil
}
