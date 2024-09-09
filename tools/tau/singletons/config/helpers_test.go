package config_test

import (
	"os"
	"path"

	"github.com/taubyte/tau/pkg/cli/common"
	"github.com/taubyte/tau/tools/tau/constants"
	"github.com/taubyte/tau/tools/tau/singletons/config"
)

var (
	testConfigName = "_fakeroot/tau.yaml"
)

func initializeTest() (cwd string, deferment func(), err error) {
	err = os.Mkdir("_fakeroot", common.DefaultDirPermission)
	if err != nil {
		return
	}

	oldFileName := constants.TauConfigFileName

	cwd, err = os.Getwd()
	if err != nil {
		return
	}

	constants.TauConfigFileName = path.Join(cwd, testConfigName)

	return cwd, func() {
		constants.TauConfigFileName = oldFileName
		config.SetConfigNil()
		os.RemoveAll("_fakeroot")
	}, nil
}

func readConfig() (string, error) {
	data, err := os.ReadFile(testConfigName)
	if err != nil {
		return "", err
	}

	return string(data), nil
}
