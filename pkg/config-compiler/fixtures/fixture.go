package fixtures

import (
	"fmt"

	"github.com/spf13/afero"
)

const rootDir = "/test_project/config/"

func VirtualFSWithBuiltProject() (afero.Fs, error) {
	fs := afero.NewMemMapFs()
	err := fs.MkdirAll(rootDir, 0750)
	if err != nil {
		return nil, fmt.Errorf("make dir failed with: %v", err)
	}

	project, err := writeProject(fs)
	if err != nil {
		return nil, fmt.Errorf("write project failed with: %v", err)
	}

	appName := testAppName
	err = writeApplication(appName, project)
	if err != nil {
		return nil, fmt.Errorf("write application failed with: %v", err)
	}

	fs, err = writeDatabase(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write databases failed with: %v", err)
	}

	fs, err = writeDomain(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write domains failed with: %v", err)
	}

	fs, err = writeFunction(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write functions failed with: %v", err)
	}

	fs, err = writeLibrary(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write libraries failed with: %v", err)
	}

	fs, err = writeMessaging(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write messaging failed with: %v", err)
	}

	fs, err = writeService(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write services failed with: %v", err)
	}

	fs, err = writesmartOp(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write smart-ops failed with: %v", err)
	}

	fs, err = writeStorage(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write storages failed with: %v", err)
	}

	fs, err = writeWebsite(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write websites failed with: %v", err)
	}

	return fs, nil
}
