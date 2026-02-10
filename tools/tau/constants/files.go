package constants

import (
	"os"
	"path"
)

var (
	TauConfigFileName string
)

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		panic("trying to find your home directory failed with:" + err.Error())
	}
	TauConfigFileName = path.Join(home, "tau")
}
