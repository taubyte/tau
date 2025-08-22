package config

import (
	"path/filepath"
	"strings"

	seer "github.com/taubyte/tau/pkg/yaseer"
	"github.com/taubyte/tau/tools/tau/constants"
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
