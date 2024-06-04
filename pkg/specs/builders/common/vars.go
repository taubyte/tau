package common

import "time"

const (
	DockerDir             = "Docker"
	TaubyteDir            = ".taubyte"
	DepreciatedTaubyteDir = "taubyte"
	Dockerfile            = "Dockerfile"
	ScriptExtension       = ".sh"
	ConfigFile            = "config.yaml"
)

var (
	ImageCleanInterval = 24 * time.Hour
	ImageCleanAge      = 7 * ImageCleanInterval
	DefaultTime        = time.Unix(0, 0)

	defaultWDError = "setting working directory failed with: %s"
)
