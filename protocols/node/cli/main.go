package main

import (
	"os"
)

func main() {

	initLogger()
	initContext()

	if err := defineCLI().Run(os.Args); err != nil {
		Logger.Error(err.Error())
	}

	os.Exit(0)
}
