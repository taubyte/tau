package common

import (
	"github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
)

func init() {
	config.WithOptions(config.ParseEnv)
	config.AddDriver(yaml.Driver)
}
