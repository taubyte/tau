package config

import (
	"path/filepath"
	"strings"

	seer "github.com/taubyte/go-seer"
	"github.com/taubyte/tau/tools/tau/constants"

	// Importing to run the common initialization
	_ "github.com/taubyte/tau/tools/tau/singletons/common"
)

var _config *tauConfig

func Clear() {
	_config = nil
}

func getOrCreateConfig() *tauConfig {
	if _config == nil {
		err := loadConfig()
		if err != nil {
			panic(err)
		}
	}

	return _config
}

func (*tauConfig) Document() *seer.Query {
	configBaseName := strings.TrimSuffix(filepath.Base(constants.TauConfigFileName), ".yaml")
	return _config.root.Get(configBaseName).Document().Fork()
}
