package node

import (
	"os"

	"github.com/ipfs/go-log/v2"
)

func setLevel() {
	lvlstr, ok := os.LookupEnv("LOG_LEVEL")
	// LOG_LEVEL not set, let's default to debug
	if !ok {
		lvlstr = "error"
	}

	lvl, err := log.LevelFromString(lvlstr)
	if err != nil {
		panic(err)
	}

	// set global log level
	log.SetAllLoggers(lvl)
}
