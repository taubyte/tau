package node

import (
	"fmt"
	"os"

	"github.com/ipfs/go-log/v2"
)

func setLogLevel() {
	lvlstr, ok := os.LookupEnv("LOG_LEVEL")
	// LOG_LEVEL not set, let's default to info
	var source string
	if !ok {
		lvlstr = "info"
		source = "default"
	} else {
		source = "env"
	}

	fmt.Printf("[LOG] Log level set to: %s (source: %s)\n", lvlstr, source)

	if _, err := log.LevelFromString(lvlstr); err != nil {
		panic(err)
	}

	log.SetLogLevelRegex("tau\\..*", lvlstr)
}
