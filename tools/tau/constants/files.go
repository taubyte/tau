package constants

import (
	"os"
	"path"
)

var (
	TauConfigFileName string
)

func init() {
	if e := os.Getenv("TAU_CONFIG_FILE"); e != "" {
		TauConfigFileName = e
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		panic("trying to find your home directory failed with:" + err.Error())
	}
	TauConfigFileName = path.Join(home, "tau")
}
